package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndCreatesLocalServer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "islet.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(s.ServerID) != 32 {
		t.Fatalf("server id = %q, want 32 hex chars", s.ServerID)
	}
	if s.Hostname == "" {
		t.Fatal("hostname is empty")
	}
	var version int
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version < 1 {
		t.Fatalf("schema version = %d, want >= 1", version)
	}
	if err := s.Audit(ctx, "test", "unit.test", "", ""); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if err := s.SetSetting(ctx, "k", "v1"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatalf("update setting: %v", err)
	}
	v, ok, err := s.Setting(ctx, "k")
	if err != nil || !ok || v != "v2" {
		t.Fatalf("setting = %q ok=%v err=%v, want v2", v, ok, err)
	}
	first := s.ServerID
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening must keep the same local server and not re-run migrations.
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.ServerID != first {
		t.Fatalf("server id changed on reopen: %s -> %s", first, s2.ServerID)
	}
	var n int
	if err := s2.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE is_local = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("local servers = %d err=%v, want 1", n, err)
	}
}

func TestMigrationsAreOrderedAndUnique(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 || migs[0].version != 1 {
		t.Fatalf("first migration = %+v, want version 1", migs)
	}
	for i := 1; i < len(migs); i++ {
		if migs[i].version != migs[i-1].version+1 {
			t.Fatalf("migration versions must be consecutive: %d then %d", migs[i-1].version, migs[i].version)
		}
	}
}
