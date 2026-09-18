package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
)

// unitName is the transient systemd unit the tmux server runs in.
//
// It carries a digest of the socket path rather than being a constant, because
// a development daemon beside the installed one is the normal way to work on
// this project — the data directory differs, so the socket does, so the servers
// are genuinely separate. With one fixed name the second daemon is refused
// ("Unit islet-tmux.service already exists"), falls back, and starts its server
// inside itself: the exact placement this file exists to avoid, reintroduced by
// the presence of another copy of Islet on the same host.
func (s *Service) unitName() string {
	sum := sha256.Sum256([]byte(s.sock))
	return "islet-tmux-" + hex.EncodeToString(sum[:4])
}

// serverUp reports whether a tmux server is listening on the socket.
//
// It dials the socket rather than asking tmux, because every tmux client
// command starts a server when none is running — so probing with tmux is what
// creates the thing being probed, in whatever place the probe happened to run.
// That is the whole bug this file exists to remove.
func (s *Service) serverUp() bool {
	// A timeout, and then a second look before believing the answer.
	//
	// This is not a status query. It decides whether to run a recovery that
	// begins by stopping the unit the server lives in, so a false negative
	// destroys every workspace on the machine and every agent inside them. That
	// happened: a 500 ms dial on a box under memory pressure — where the tmux
	// server itself held a gigabyte — timed out, and the panel tore the server
	// down and rebuilt it eleven times in one minute, each time killing the
	// session somebody was working in.
	//
	// So the deadline is generous and a failure is checked again. A dial that
	// fails twice, two seconds apart, is a server that is genuinely not there;
	// one slow dial is a busy machine.
	if dialOK(s.sock, 2*time.Second) {
		return true
	}
	if errors.Is(dialErr(s.sock), syscall.ECONNREFUSED) {
		return false // answered, and said no: nothing is listening
	}
	time.Sleep(250 * time.Millisecond)
	return dialOK(s.sock, 2*time.Second)
}

func dialOK(sock string, d time.Duration) bool {
	c, err := net.DialTimeout("unix", sock, d)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func dialErr(sock string) error {
	c, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err == nil {
		_ = c.Close()
	}
	return err
}

// stale reports a socket file left behind by a server that is gone: the path
// exists but nothing answers on it. tmux refuses to start on such a file.
func (s *Service) stale() bool {
	if _, err := os.Stat(s.sock); err != nil {
		return false
	}
	_, err := net.DialTimeout("unix", s.sock, 500*time.Millisecond)
	return errors.Is(err, syscall.ECONNREFUSED)
}

// unitTasks is how many processes are in the tmux server's cgroup.
//
// systemd answers this from the cgroup itself, so unlike every socket probe it
// cannot be fooled by a server that is merely busy. Zero means the unit holds
// nothing — either it was never started or whatever it held has gone — and only
// then is it safe to clear the unit and build a new one.
//
// A host without systemd, or a unit that does not exist, answers "0" or an
// error; both mean "nothing of ours is running", which is the honest reading.
// RemainAfterExit keeps the unit active long after its ExecStart has exited, so
// `is-active` says "active" for a server that died and cannot be used here.
func (s *Service) unitTasks(ctx context.Context) int {
	res, err := s.run.Read(ctx, "systemctl", "show", s.unitName()+".service", "-p", "TasksCurrent", "--value")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		return 0
	}
	return n
}

// ensureServer starts the tmux server, once, outside this process.
//
// tmux starts a server on demand as a child of whoever ran the first command.
// Run from isletd that put the server in isletd's control group and isletd's
// mount namespace, which is why restarting the daemon has been able to reach
// it at all: KillMode decided whether it was killed, PrivateTmp decided whether
// its /tmp survived, and both are questions that should never have been asked.
// A terminal multiplexer does not belong to the program that draws its UI.
//
// systemd-run asks PID 1 to start it instead, in a transient unit of its own.
// The server is then a sibling of isletd rather than a child: restarting,
// updating or stopping the daemon does not touch it, it holds the host's /tmp,
// and `tmux -S <sock> attach` from an SSH shell reaches the same server it
// always did. Nothing about the socket path or the sessions changes.
//
// Without systemd — a developer's machine — the old behaviour is kept: the next
// tmux command starts the server inline, which is fine when nothing is going to
// restart underneath it.
func (s *Service) ensureServer(ctx context.Context, actor string) {
	if s.serverUp() {
		return // the common case, and it costs one socket dial
	}
	// Only one attempt at a time. Without this every concurrent panel action
	// races to create a server on the same socket; on this machine that
	// segfaulted tmux and left the transient unit holding its own name, after
	// which every later attempt failed with "already exists" and fell back to
	// starting the server inside isletd — the placement being avoided.
	s.serverMu.Lock()
	defer s.serverMu.Unlock()
	if s.serverUp() {
		return // someone else won the race and started it
	}
	// A start that just failed is not retried on the next request. Without
	// this, a host where the server cannot start — no systemd, a broken tmux,
	// a full disk — runs the whole recovery sequence for every action, which is
	// three processes each time and a command log made of nothing else.
	if time.Since(s.lastStartFailed) < startRetryAfter {
		return
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return // no tmux at all; the caller's command will report it
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return // not a systemd host: let tmux start the server itself
	}

	// A unit left over from a server that has since died keeps its name, and
	// systemd-run refuses to reuse it. Clearing it is only safe because no
	// server is answering — and that condition is now checked here rather than
	// assumed from a probe taken further up.
	//
	// It was assumed, and the assumption was wrong under load: stopping this
	// unit kills the tmux server and with it every workspace session and every
	// agent running in one. That is far too destructive to reach on a stale
	// reading, so it is the last thing tried and the socket is asked once more
	// immediately before.
	unit := s.unitName()
	// Ask systemd whether a server is alive before doing anything that would
	// end one. This is the check that matters, and dialling the socket is not
	// it: a unix socket whose listener is alive but whose accept queue is full
	// refuses connections, which is exactly what a busy tmux server does — so
	// every probe in this file reads "dead" under precisely the load that makes
	// killing it worst.
	//
	// The unit's task count cannot be wrong that way. It is the number of
	// processes in the cgroup, answered by pid 1, and if it is above zero there
	// is a tmux server in there holding somebody's work.
	if n := s.unitTasks(ctx); n > 0 {
		return
	}
	// Only now, with nothing alive in the unit, is a leftover socket file
	// genuinely leftover. Removing it earlier is what made the previous attempt
	// at this fix useless: the file went first, and every check after it was
	// then asking about a path that no longer existed.
	if s.stale() {
		_ = os.Remove(s.sock)
	}
	if dialOK(s.sock, 2*time.Second) {
		return // something is answering after all; leave it alone
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/root" // the daemon's unit sets none, and tmux wants one
	}
	// Type=oneshot with RemainAfterExit is what holds this: tmux's client forks
	// the server and exits, and there is no pid file, so Type=forking leaves
	// systemd unable to find a main process — it marks the unit dead,
	// --collect removes it, and the server is left in whatever cgroup it
	// started from. Measured, not assumed: with forking the server came back up
	// inside isletd.service, the cgroup this exists to leave.
	//
	// `exit-empty off` is the other half. A tmux server with no sessions exits
	// at once, so starting one and creating sessions afterwards races its own
	// shutdown. It is a server option, so it is set with -s.
	start := func() (cmdrun.Result, error) {
		return s.run.Run(ctx, actor, "systemd-run",
			"--collect",
			"--unit="+unit,
			"--description=Islet workspace tmux server",
			"--property=Type=oneshot",
			"--property=RemainAfterExit=yes",
			"--property=KillMode=process",
			"--setenv=HOME="+home,
			tmuxPath, "-S", s.sock, "start-server", ";", "set", "-s", "exit-empty", "off")
	}
	out, err := start()
	// Clearing the unit is a last resort, not an opening move.
	//
	// It used to run first, every time, on the theory that a leftover name
	// would otherwise refuse the start — and stopping that unit is what kills a
	// tmux server and everything in it. So it is only reached when the start
	// actually fails for that reason, which is the only case it was ever for.
	// --collect unloads a unit that finished, so the usual path never needs it.
	if err != nil && strings.Contains(out.Stdout+out.Stderr, "already exists") {
		_, _ = s.run.Run(ctx, actor, "systemctl", "stop", unit+".service")
		_, _ = s.run.Run(ctx, actor, "systemctl", "reset-failed", unit+".service")
		out, err = start()
	}
	if err != nil {
		s.lastStartFailed = time.Now()
		s.log.Warn("workspaces: could not start the tmux server under systemd; the next command will start one here instead",
			"err", err, "output", strings.TrimSpace(out.Stdout+out.Stderr))
		return
	}
	for i := 0; i < 40; i++ {
		if s.serverUp() {
			s.log.Info("workspaces: tmux server started in its own systemd unit", "unit", unit, "socket", s.sock)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.lastStartFailed = time.Now()
	s.log.Warn("workspaces: the tmux server did not come up on the socket", "socket", s.sock, "unit", unit)
}

// How long to leave a failed start alone. Long enough that a broken host is not
// running three processes per request, short enough that fixing it shows up
// while somebody is still looking at the page.
const startRetryAfter = 30 * time.Second
