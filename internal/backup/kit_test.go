package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

// What the check can decide without opening a repository: a file that is not
// a kit, a repository a destination here points at with the same or another
// key, a folder that is not on this machine, and a destination here the kit
// would not restore.
func TestCheckKitWithoutRestic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	keys, err := auth.LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, keys, nil, nil, nil, dir, nil)
	ins := func(name, path, pw string) {
		cfg, _ := json.Marshal(map[string]string{"path": path})
		c, _ := keys.Encrypt(cfg)
		p, _ := keys.Encrypt([]byte(pw))
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO backup_destinations (id, server_id, name, type, config, password) VALUES (?, ?, ?, 'local', ?, ?)`, name, st.ServerID, name, c, p); err != nil {
			t.Fatal(err)
		}
	}
	ins("gone", "/nonexistent/islet-a", "key-one")
	ins("moved", "/nonexistent/islet-b", "key-now")
	ins("extra", "/nonexistent/islet-c", "key-c")

	if _, err := s.CheckKit(ctx, "admin", []byte(`{"hello":"world"}`)); err != ErrNotAKit {
		t.Fatalf("a JSON file that is not a kit: %v", err)
	}
	kit, _ := json.Marshal(map[string]any{"islet": "recovery kit", "generatedAt": "2026-01-02T03:04:05Z", "destinations": []map[string]any{
		{"name": "gone", "type": "local", "config": map[string]string{"path": "/nonexistent/islet-a"}, "password": "key-one"},
		{"name": "moved", "type": "local", "config": map[string]string{"path": "/nonexistent/islet-b"}, "password": "key-then"},
		{"name": "broken", "type": "s3", "config": map[string]string{"bucket": "b"}},
	}})
	rep, err := s.CheckKit(ctx, "admin", kit)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Destinations) != 3 || rep.GeneratedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("report = %+v", rep)
	}
	a, b, c := rep.Destinations[0], rep.Destinations[1], rep.Destinations[2]
	if a.Here != "same" || a.HereName != "gone" || a.OK || a.Error == "" {
		t.Errorf("same key, missing folder: %+v", a)
	}
	if b.Here != "other-key" {
		t.Errorf("a key that changed since the kit: %+v", b)
	}
	if c.OK || c.Error == "" || c.Repo != "" {
		t.Errorf("an incomplete destination: %+v", c)
	}
	if len(rep.Uncovered) != 1 || rep.Uncovered[0] != "extra" {
		t.Errorf("uncovered = %v", rep.Uncovered)
	}
}

func TestResticJSONErrorsReadAsText(t *testing.T) {
	out := "some progress\n{\"message_type\":\"exit_error\",\"code\":12,\"message\":\"Fatal: wrong password or no key found\"}\n"
	if got := lastLine(out); got != "wrong password or no key found" {
		t.Errorf("lastLine = %q", got)
	}
	if got := lastLine("Fatal: unable to open config file\n"); got != "Fatal: unable to open config file" {
		t.Errorf("plain text changed: %q", got)
	}
}
