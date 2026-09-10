//go:build !windows

package cron

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the job in its own process group so a timeout kills
// children too, not only the shell.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
}
