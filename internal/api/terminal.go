package api

import (
	"context"
	"net/http"
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
	s.servePTY(w, r, terminal.Options{}, "terminal", "")
}

func contextWithCancel(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(r.Context())
}

func contextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func contextBackground() context.Context { return context.Background() }
