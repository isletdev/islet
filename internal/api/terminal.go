package api

import (
	"context"
	"github.com/isletdev/islet/internal/files"
	"net/http"
	"os"
	"time"

	"github.com/isletdev/islet/internal/terminal"
	"github.com/isletdev/islet/pkg/api"
)

// Client-to-server control messages are JSON text frames; keystrokes are
// binary frames. Server-to-client output is always binary.
type termControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can open a terminal"})
		return
	}
	// "Open in terminal" on a folder sends the path, so the shell starts there
	// instead of the home directory. It is validated the same way every other
	// path is, and a directory that is not there falls back to the default.
	opts := terminal.Options{}
	if dir := r.URL.Query().Get("dir"); dir != "" {
		clean, err := files.Clean(dir)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		if fi, err := os.Stat(clean); err == nil && fi.IsDir() {
			opts.Dir = clean
		}
	}
	s.servePTY(w, r, opts, "terminal", "")
}

func contextWithCancel(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(r.Context())
}

func contextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func contextBackground() context.Context { return context.Background() }
