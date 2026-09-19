//go:build !windows

package assistant

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts Claude Code in a process group of its own, so stopping a
// run kills what it started as well as the binary itself.
//
// It matters more here than it looks. Claude Code launches an MCP server as a
// child, and that child inherits the pipe this process reads the answer from.
// Killing only the parent leaves the child holding the write end open, so the
// read never sees end-of-file and the run hangs until the thirty-minute
// ceiling — with the conversation refusing new questions the whole time
// because one is still "working". Killing the group closes it.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree ends the process and everything it started.
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// The negative pid is the group. If that fails — a kernel without it, or a
	// process that got away — fall back to the one process we know of.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
