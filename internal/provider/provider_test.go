package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

func TestSnapshotFlow(t *testing.T) {
	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/servers":
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": []map[string]any{
				{"id": 1, "name": "other", "public_net": map[string]any{"ipv4": map[string]any{"ip": "10.9.9.9"}}},
				{"id": 42, "name": "box", "public_net": map[string]any{"ipv4": map[string]any{"ip": "203.0.113.7"}}},
			}})
		case r.URL.Path == "/servers/42/actions/create_image" && r.Method == http.MethodPost:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			created = append(created, body["description"])
			_ = json.NewEncoder(w).Encode(map[string]any{"action": map[string]any{"id": 7}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	st, err := store.Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	keys, err := auth.LoadOrCreateKeys(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, keys)
	s.Base = srv.URL
	s.PublicIP = func(context.Context) string { return "203.0.113.7" }
	ctx := context.Background()
	if _, err := s.Snapshot(ctx, "t", "test"); err != ErrNotConfigured {
		t.Fatal("expected not configured", err)
	}
	if err := s.SetToken(ctx, "t", "hetzner", "bad"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatal("bad token should fail", err)
	}
	if err := s.SetToken(ctx, "t", "hetzner", "good"); err != nil {
		t.Fatal(err)
	}
	if !s.State(ctx).Configured {
		t.Fatal("should be configured")
	}
	desc, err := s.Snapshot(ctx, "t", "ssh change")
	if err != nil || !strings.HasPrefix(desc, "islet-ssh-change-") || len(created) != 1 {
		t.Fatal(desc, err, created)
	}
	if st := s.State(ctx); st.LastReason != "ssh change" || st.LastSnapshot == "" {
		t.Fatalf("%+v", st)
	}
	if err := s.SetToken(ctx, "t", "", ""); err != nil || s.State(ctx).Configured {
		t.Fatal("clear failed")
	}
}
