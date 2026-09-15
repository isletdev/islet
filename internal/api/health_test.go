package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/pkg/api"
)

// A health check has to answer without authentication — that is what it is for.
// What it must not do is tell a stranger which version to look up on a list of
// known holes, or what the machine is called.
func TestHealthTellsAStrangerOnlyThatItIsUp(t *testing.T) {
	s := &Server{store: &store.Store{ServerID: "srv-1", Hostname: "box-1"}, started: time.Now()}

	w := httptest.NewRecorder()
	s.handleHealth(w, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	var h api.Health
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" {
		t.Errorf("status %q — a monitor has to be able to read this", h.Status)
	}
	for name, got := range map[string]string{
		"version": h.Version, "commit": h.Commit, "serverId": h.ServerID, "hostname": h.Hostname,
	} {
		if got != "" {
			t.Errorf("%s was given to an unauthenticated caller: %q", name, got)
		}
	}
	if h.UptimeSeconds != 0 {
		t.Errorf("uptime was given away: %d", h.UptimeSeconds)
	}

	// Signed in, the same endpoint says which daemon this is — the panel's
	// header shows the version and `islet status` prints the host.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxUser, &auth.User{Username: "alice", Role: "admin"}))
	w = httptest.NewRecorder()
	s.handleHealth(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.Hostname != "box-1" || h.ServerID != "srv-1" {
		t.Errorf("a signed-in caller was not told which server this is: %+v", h)
	}

	// And so does the local socket, which is how `islet status` works as root
	// on the server itself.
	r = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r = r.WithContext(LocalConn(r.Context()))
	w = httptest.NewRecorder()
	s.handleHealth(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.Hostname != "box-1" {
		t.Errorf("the local socket was treated as a stranger: %+v", h)
	}
}
