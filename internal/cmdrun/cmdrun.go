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
func (r *Runner) RunInput(ctx context.Context, actor string, stdin []byte, name string, args ...string) (Result, error) {
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
	r.record(ctx, actor, name, args, res)
	if res.ExitCode != 0 {
		return res, &Error{Cmd: Display(name, args...), Result: res}
	}
	return res, nil
}

// Stream starts a long-running command and returns its combined output
// reader plus a wait function. The call is recorded when it finishes.
func (r *Runner) Stream(ctx context.Context, actor string, name string, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, name, args...)
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

// Display renders a command line the way a person would type it.
func Display(name string, args ...string) string {
	parts := []string{name}
	for _, a := range args {
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
