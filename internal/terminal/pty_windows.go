//go:build windows

package terminal

import (
	"os"
	"strings"

	"github.com/UserExistsError/conpty"
)

type winSession struct {
	c *conpty.ConPty
}

// Start launches PowerShell on a ConPTY. This exists so Islet can be
// developed on Windows; production runs on Linux.
func Start(o Options) (Session, error) {
	o.defaults()
	shell := o.Shell
	if shell == "" {
		shell = "powershell.exe -NoLogo"
	}
	if len(o.Command) > 0 {
		shell = strings.Join(o.Command, " ")
	}
	dir := o.Dir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	c, err := conpty.Start(shell, conpty.ConPtyDimensions(o.Cols, o.Rows), conpty.ConPtyWorkDir(dir))
	if err != nil {
		return nil, err
	}
	return &winSession{c: c}, nil
}

func (s *winSession) Read(p []byte) (int, error)  { return s.c.Read(p) }
func (s *winSession) Write(p []byte) (int, error) { return s.c.Write(p) }
func (s *winSession) Resize(cols, rows int) error { return s.c.Resize(cols, rows) }
func (s *winSession) Close() error                { return s.c.Close() }
