//go:build !windows

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixSession struct {
	f   *os.File
	cmd *exec.Cmd
}

// Start launches a login shell on a PTY.
func Start(o Options) (Session, error) {
	o.defaults()
	shell := o.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			if _, err := os.Stat("/bin/bash"); err == nil {
				shell = "/bin/bash"
			} else {
				shell = "/bin/sh"
			}
		}
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "ISLET_TERMINAL=1")
	if o.Dir != "" {
		cmd.Dir = o.Dir
	} else if home := os.Getenv("HOME"); home != "" {
		cmd.Dir = home
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(o.Cols), Rows: uint16(o.Rows)})
	if err != nil {
		return nil, err
	}
	return &unixSession{f: f, cmd: cmd}, nil
}

func (s *unixSession) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *unixSession) Write(p []byte) (int, error) { return s.f.Write(p) }

func (s *unixSession) Resize(cols, rows int) error {
	return pty.Setsize(s.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *unixSession) Close() error {
	_ = s.f.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_, _ = s.cmd.Process.Wait()
	return nil
}
