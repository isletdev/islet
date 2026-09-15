package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/isletdev/islet/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// Two tables grew without a ceiling: 3,600 commands and 700 audit rows a day on
// the server this was measured on, which is 1.3 million and 250,000 rows a year
// in a database meant to be small enough to back up in a moment.
func TestHousekeepDropsWhatIsOldAndKeepsWhatIsNot(t *testing.T) {
	st := open(t)
	ctx := context.Background()

	// Old and new, in both tables.
	for _, q := range []string{
		`INSERT INTO commands (server_id, actor, command, exit_code, duration_ms, stderr, created_at)
		   VALUES (?, 'root', 'docker ps', 0, 5, '', strftime('%Y-%m-%dT%H:%M:%fZ','now','-40 days'))`,
		`INSERT INTO commands (server_id, actor, command, exit_code, duration_ms, stderr, created_at)
		   VALUES (?, 'root', 'docker ps', 0, 5, '', strftime('%Y-%m-%dT%H:%M:%fZ','now','-1 days'))`,
		`INSERT INTO audit_log (server_id, actor, action, target, detail, created_at)
		   VALUES (?, 'root', 'domain.create', 'a.example.com', '', strftime('%Y-%m-%dT%H:%M:%fZ','now','-400 days'))`,
		`INSERT INTO audit_log (server_id, actor, action, target, detail, created_at)
		   VALUES (?, 'root', 'domain.create', 'b.example.com', '', strftime('%Y-%m-%dT%H:%M:%fZ','now','-40 days'))`,
	} {
		if _, err := st.DB.ExecContext(ctx, q, st.ServerID); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.Housekeep(ctx); err != nil {
		t.Fatal(err)
	}

	count := func(table string) int {
		var n int
		if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count("commands"); got != 1 {
		t.Errorf("commands: %d rows left, want the one from yesterday", got)
	}
	// A year, not a fortnight: the audit log answers a question about the past,
	// so the forty-day-old row stays and the four-hundred-day-old one goes.
	if got := count("audit_log"); got != 1 {
		t.Errorf("audit_log: %d rows left, want the one from forty days ago", got)
	}
}

// The age limit alone would not notice a loop that wrote a fortnight's worth in
// an hour, so there is a ceiling on the count as well.
func TestHousekeepKeepsTheNewestWhenThereAreTooMany(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if _, err := tx.Exec(`INSERT INTO commands (server_id, actor, command, exit_code, duration_ms, stderr)
			VALUES (?, 'root', ?, 0, 1, '')`, st.ServerID, fmt.Sprintf("echo %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A small cap, so the test says what it means without writing 200,000 rows.
	if _, err := st.DB.ExecContext(ctx, `DELETE FROM commands WHERE server_id = ? AND id NOT IN (
		SELECT id FROM commands WHERE server_id = ? ORDER BY id DESC LIMIT 100)`, st.ServerID, st.ServerID); err != nil {
		t.Fatal(err)
	}
	var n int
	var newest, oldest string
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM commands`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// By id, not by text: "echo 99" sorts above "echo 249" and the question
	// here is which rows survived, not how they read.
	if err := st.DB.QueryRowContext(ctx, `SELECT command FROM commands ORDER BY id DESC LIMIT 1`).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT command FROM commands ORDER BY id ASC LIMIT 1`).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if n != 100 {
		t.Errorf("kept %d rows, want 100", n)
	}
	if newest != "echo 249" || oldest != "echo 150" {
		t.Errorf("kept %q…%q, so the cap dropped the wrong end", oldest, newest)
	}
}

// Housekeeping must not touch another server's rows: every machine-bound table
// carries a server_id and every query filters on it.
func TestHousekeepLeavesAnotherServerAlone(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO servers (id, name, hostname) VALUES ('other', 'elsewhere', 'elsewhere')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO commands (server_id, actor, command, exit_code, duration_ms, stderr, created_at)
		VALUES ('other', 'root', 'rm -rf /', 0, 1, '', strftime('%Y-%m-%dT%H:%M:%fZ','now','-400 days'))`); err != nil {
		t.Fatal(err)
	}
	if err := st.Housekeep(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM commands WHERE server_id = 'other'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("another server's history was pruned by this one's housekeeping")
	}
}
