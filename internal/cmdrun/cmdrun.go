// Package cmdrun is the one place the daemon executes external commands.
// Every run is recorded (command, exit code, duration, who asked) so the
// command transparency drawer can show users exactly what Islet did on their
// server.
package cmdrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/store"
)

// Runner executes commands and records them.
type Runner struct {
	st  *store.Store
	log *slog.Logger
}

// New builds a runner.
func New(st *store.Store, log *slog.Logger) *Runner { return &Runner{st: st, log: log} }

// Result is what a finished command produced.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Error is returned for a non-zero exit, carrying the output.
type Error struct {
	Cmd    string
	Result Result
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Result.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Result.Stdout)
	}
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Sprintf("%s: exit %d: %s", e.Cmd, e.Result.ExitCode, msg)
}

// Run executes name with args, records the call under actor, and returns
// the output. Stdout is capped at 16 MB.
func (r *Runner) Run(ctx context.Context, actor string, name string, args ...string) (Result, error) {
	return r.RunInput(ctx, actor, nil, name, args...)
}

// RunInput is Run with data on stdin.
// Read runs a command that only observes, without writing it down.
//
// The drawer this package feeds answers one question: what has this panel done
// to my server. Reading the answer to that question is not part of it, and on a
// real server the reads drown everything else — three days of this machine held
// 8,681 commands, of which 5,300 were the workspaces page asking tmux what it
// was doing every ten seconds and 1,200 were looking up where `claude` lives.
// What somebody actually changed was underneath all of it.
//
// So: anything that could alter the machine goes through Run and is recorded,
// always. Read is for `tmux list-windows`, `command -v`, `tmux -V` — commands
// whose only effect is to tell us something. It is a small door and it is worth
// keeping small: if a call through here can change anything, it is in the wrong
// place.
func (r *Runner) Read(ctx context.Context, name string, args ...string) (Result, error) {
	return r.exec(ctx, "", nil, false, name, args...)
}

func (r *Runner) RunInput(ctx context.Context, actor string, stdin []byte, name string, args ...string) (Result, error) {
	return r.exec(ctx, actor, stdin, true, name, args...)
}

func (r *Runner) exec(ctx context.Context, actor string, stdin []byte, record bool, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, n: 16 << 20}
	cmd.Stderr = &limitedWriter{w: &errb, n: 1 << 20}
	start := time.Now()
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String(), Duration: time.Since(start)}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.ExitCode = -1
		res.Stderr = err.Error()
	}
	if record {
		r.record(ctx, actor, name, args, res)
	} else {
		r.log.Debug("read", "cmd", Display(name, args...), "exit", res.ExitCode, "ms", res.Duration.Milliseconds())
	}
	if res.ExitCode != 0 {
		return res, &Error{Cmd: Display(name, args...), Result: res}
	}
	return res, nil
}

// Stream starts a long-running command and returns its combined output
// reader plus a wait function. The call is recorded when it finishes.
func (r *Runner) Stream(ctx context.Context, actor string, name string, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Once the reader is closed the writes fail, and without a delay Wait would
	// block until the child noticed on its own. This bounds it.
	cmd.WaitDelay = 5 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	start := time.Now()
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			code = -1
		}
		r.record(context.Background(), actor, name, args, Result{ExitCode: code, Duration: time.Since(start)})
		done <- err
	}()
	return pr, func() error { return <-done }, nil
}

func (r *Runner) record(ctx context.Context, actor, name string, args []string, res Result) {
	line := Display(name, args...)
	r.log.Debug("cmd", "actor", actor, "cmd", line, "exit", res.ExitCode, "ms", res.Duration.Milliseconds())
	_, err := r.st.DB.ExecContext(ctx, `INSERT INTO commands (server_id, actor, command, exit_code, duration_ms, stderr)
		VALUES (?, ?, ?, ?, ?, ?)`, r.st.ServerID, actor, line, res.ExitCode, res.Duration.Milliseconds(), truncate(res.Stderr, 2000))
	if err != nil {
		r.log.Warn("record command failed", "err", err)
	}
}

// Record stores a command that was run outside the Runner (piped dumps),
// so the transparency drawer stays complete.
func (r *Runner) Record(ctx context.Context, actor, line string, res Result) {
	r.record(ctx, actor, line, nil, res)
}

// Names whose value must never be written down. Islet passes the restic
// repository password, cloud access keys, registry and runner tokens and
// database passwords to containers as environment variables, and every command
// it runs is recorded for the transparency drawer and quoted back in errors.
// Without this the drawer hands out the password protecting the backups.
var secretName = regexp.MustCompile(`(?i)(PASSWORD|PASSWD|_PWD|SECRET|TOKEN|APIKEY|API_KEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIAL|_AUTH)`)

// Credentials inside a connection string, such as a restic REST or S3 URL.
var secretInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@\s]+):[^@\s]+@`)

// secretFlag matches a long option whose name says the argument after it is a
// secret, so that `--token abc` loses the abc. The whole name has to be one of
// these words, optionally prefixed: --gitlab-token counts, --authentication
// Database does not, and neither does --passthrough.
//
// Long options only. A short one cannot be read without knowing the command:
// -p is a password to mysql, a published port to `docker run`, a project to
// `docker compose` and a property to timedatectl, and the commands here arrive
// wrapped in `docker exec`, so the program being run is not even the first
// word. Redacting every -p would hide far more than it protects, which is why
// the call sites that had one now pass the long form instead.
var secretFlag = regexp.MustCompile(`^--(?i:[a-z0-9]+-)*(?i:pass|passwd|password|pwd|secret|token|apikey|api-key|accesskey|access-key|privatekey|private-key|credential|credentials|auth)$`)

// secretInQuoted blanks a quoted value under a secret-looking key *inside* one
// argument. Creating a Mongo database runs
//
//	mongosh --eval 'db.getSiblingDB("shop").createUser({user: "shop", pwd: "…"})'
//
// and the whole script is one argument, so neither the NAME=value rule nor the
// flag rule sees the password in it — it reached the audit table and the
// command drawer in full, which is the one thing this package exists to stop.
//
// The key must be a whole word, so "passthrough" and "tokenizer" are left
// alone, and only a quoted value is touched, so prose survives.
var secretInQuoted = regexp.MustCompile(`(?i)\b(pwd|pass|passwd|password|secret|token|apikey|api_key)\b(["']?\s*[:=]\s*)(["'])(?:[^"'\\]|\\.)*["']`)

const redacted = "<redacted>"

// Redact removes a secret value from one argument, keeping the name so the
// command still reads as what it was.
func Redact(arg string) string {
	if k, v, ok := strings.Cut(arg, "="); ok && v != "" && secretName.MatchString(k) {
		return k + "=" + redacted
	}
	arg = secretInURL.ReplaceAllString(arg, "${1}:"+redacted+"@")
	return secretInQuoted.ReplaceAllStringFunc(arg, func(m string) string {
		g := secretInQuoted.FindStringSubmatch(m)
		return g[1] + g[2] + g[3] + redacted + g[3]
	})
}

// Display renders a command line the way a person would type it, with secret
// values removed. Every path that stores or shows a command goes through here,
// so there is one place to get this right.
func Display(name string, args ...string) string {
	parts := []string{name}
	// A secret given as its own argument is only recognisable from the flag in
	// front of it, so the decision carries one step.
	hideNext := false
	for _, a := range args {
		if hideNext {
			a, hideNext = redacted, false
		} else {
			hideNext = secretFlag.MatchString(a)
			a = Redact(a)
		}
		if a == "" || strings.ContainsAny(a, " \t\n\"'$`") {
			parts = append(parts, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	l.n -= len(p)
	_, err := l.w.Write(p)
	return len(p), err
}
