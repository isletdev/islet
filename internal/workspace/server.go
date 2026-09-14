package workspace

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
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
		return
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
	// Type=oneshot with RemainAfterExit is what holds this: tmux's client forks
	// the server and exits, and there is no pid file, so Type=forking leaves
	// systemd unable to find a main process — it marks the unit dead, --collect
	// removes it, and the server is left in whatever cgroup it happened to be
	// in. Measured, not assumed: with forking the server came back up inside
	// isletd.service, which is the cgroup this whole change exists to leave.
	//
	// `exit-empty off` is the other half. A tmux server with no sessions exits
	// at once, so starting one and creating the sessions afterwards would race
	// against its own shutdown. With it off the server waits, and a workspace
	// session is created on it like any other.
	//
	// --collect frees the unit name once it does go away.
	out, err := s.run.Run(ctx, actor, "systemd-run",
		"--collect",
		"--unit="+unit,
		"--description=Islet workspace tmux server",
		"--property=Type=oneshot",
		"--property=RemainAfterExit=yes",
		"--property=KillMode=process",
		tmuxPath, "-S", s.sock, "start-server", ";", "set", "-g", "exit-empty", "off")
	if err != nil {
		s.log.Warn("workspaces: could not start the tmux server under systemd; falling back to an in-process one",
			"err", err, "output", out.Stdout+out.Stderr)
		return
	}
	// systemd-run returns as soon as the job is queued.
	for i := 0; i < 40; i++ {
		if s.serverUp() {
			s.log.Info("workspaces: tmux server started in its own systemd unit", "unit", unit, "socket", s.sock)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.log.Warn("workspaces: tmux server did not come up on the socket", "socket", s.sock)
}
