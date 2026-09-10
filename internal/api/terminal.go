package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

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
	// Same-origin only: the library's default rejects mismatched Origin headers.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closed")
	conn.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sess, err := terminal.Start(terminal.Options{})
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageBinary, []byte("\r\nislet: cannot start a shell: "+err.Error()+"\r\n"))
		conn.Close(websocket.StatusInternalError, "shell failed")
		return
	}
	defer sess.Close()
	_ = s.store.Audit(ctx, u.Username, "terminal.open", "", "ip="+clientIP(r))
	s.log.Info("terminal opened", "user", u.Username, "ip", clientIP(r))

	// PTY -> browser
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// browser -> PTY
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		switch typ {
		case websocket.MessageBinary:
			if _, err := sess.Write(data); err != nil {
				break
			}
		case websocket.MessageText:
			var c termControl
			if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols <= 500 && c.Rows <= 200 {
				_ = sess.Resize(c.Cols, c.Rows)
			}
		}
	}
	_ = s.store.Audit(context.Background(), u.Username, "terminal.close", "", "")
	conn.Close(websocket.StatusNormalClosure, "bye")
}
