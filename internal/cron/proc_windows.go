//go:build windows

package cron

import "os/exec"

func setProcAttrs(cmd *exec.Cmd) {}
