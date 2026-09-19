//go:build windows

package assistant

import "os/exec"

func setProcAttrs(cmd *exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
