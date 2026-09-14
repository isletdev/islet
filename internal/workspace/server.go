package workspace

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// unit is the transient systemd unit the tmux server runs in.
const unit = "islet-tmux"

// serverUp reports whether a tmux server is listening on the socket.
//
// It dials the socket rather than asking tmux, because every tmux client
// command starts a server when none is running — so probing with tmux is what
// creates the thing being probed, in whatever place the probe happened to run.
// That is the whole bug this file exists to remove.
func (s *Service) serverUp() bool {
	c, err := net.DialTimeout("unix", s.sock, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
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
	if s.stale() {
		_ = os.Remove(s.sock)
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return // no tmux at all; the caller's command will report it
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return // not a systemd host: let tmux start the server itself
	}

	// A unit left over from a server that has since died keeps its name, and
	// systemd-run refuses to reuse it. Clearing it is safe precisely because no
	// server is answering: anything it still owned would have kept the socket.
	_, _ = s.run.Run(ctx, actor, "systemctl", "stop", unit+".service")
	_, _ = s.run.Run(ctx, actor, "systemctl", "reset-failed", unit+".service")

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
	out, err := s.run.Run(ctx, actor, "systemd-run",
		"--collect",
		"--unit="+unit,
		"--description=Islet workspace tmux server",
		"--property=Type=oneshot",
		"--property=RemainAfterExit=yes",
		"--property=KillMode=process",
		"--setenv=HOME="+home,
		tmuxPath, "-S", s.sock, "start-server", ";", "set", "-s", "exit-empty", "off")
	if err != nil {
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
	s.log.Warn("workspaces: the tmux server did not come up on the socket", "socket", s.sock, "unit", unit)
}
