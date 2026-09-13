package api

import (
	"net/http"
	"testing"
)

// Browsers do not agree on how the upgrade headers are written: Firefox sends
// "Connection: keep-alive, Upgrade" and the case varies. Getting this wrong
// sends a WebSocket handshake through the ordinary proxy, where it dies without
// an error anyone can read.
func TestIsUpgrade(t *testing.T) {
	cases := []struct {
		name       string
		upgrade    string
		connection string
		want       bool
	}{
		{"chrome", "websocket", "Upgrade", true},
		{"firefox", "websocket", "keep-alive, Upgrade", true},
		{"lowercase", "WebSocket", "upgrade", true},
		{"plain request", "", "keep-alive", false},
		{"upgrade to something else", "h2c", "Upgrade", false},
		{"upgrade named but not asked for", "websocket", "keep-alive", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := http.NewRequest("GET", "/api/v1/servers/a/proxy/terminal/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.upgrade != "" {
				r.Header.Set("Upgrade", c.upgrade)
			}
			if c.connection != "" {
				r.Header.Set("Connection", c.connection)
			}
			if got := isUpgrade(r); got != c.want {
				t.Errorf("isUpgrade = %v, want %v", got, c.want)
			}
		})
	}
}
