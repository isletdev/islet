// Package terminal starts an interactive shell attached to a pseudo-terminal
// and exposes it as a byte stream with resize support. Linux uses a real PTY;
// Windows uses ConPTY so the panel can be developed on a workstation.
package terminal

import "io"

// Session is a running shell.
type Session interface {
	io.ReadWriteCloser
	// Resize changes the terminal size in character cells.
	Resize(cols, rows int) error
}

// Options tune the shell that is started.
type Options struct {
	Cols, Rows int
	// Shell overrides the default shell for the platform.
	Shell string
	// Command, when set, is run instead of a shell (argv form).
	Command []string
	// Dir is the working directory; empty means the platform default.
	Dir string
}

func (o *Options) defaults() {
	if o.Cols <= 0 {
		o.Cols = 120
	}
	if o.Rows <= 0 {
		o.Rows = 32
	}
}
