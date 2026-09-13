package sqlclient

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fakeKeys stands in for the panel's key store. It is reversible and obviously
// not real encryption; what it proves is that the store puts a secret through
// the key store on the way in and takes it back out on the way past, and that
// what lands in the column is not the password.
type fakeKeys struct{ fail bool }

func (k fakeKeys) Encrypt(b []byte) ([]byte, error) {
	if k.fail {
		return nil, errors.New("no key")
	}
	out := make([]byte, len(b)+4)
	copy(out, "ENC:")
	for i, c := range b {
		out[i+4] = c ^ 0x5a
	}
	return out, nil
}

func (k fakeKeys) Decrypt(b []byte) ([]byte, error) {
	if len(b) < 4 || string(b[:4]) != "ENC:" {
		return nil, errors.New("not encrypted by this key store")
	}
	out := make([]byte, len(b)-4)
	for i, c := range b[4:] {
		out[i] = c ^ 0x5a
	}
	return out, nil
}

func testStore(t *testing.T) (*Store, *sql.DB, *clock) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/test.db?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// The one table this migration references, as the real schema has it.
	if _, err := db.Exec(`CREATE TABLE servers (id TEXT PRIMARY KEY, name TEXT, hostname TEXT, is_local INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers (id, name, hostname, is_local) VALUES ('srv1','local','local',1)`); err != nil {
		t.Fatal(err)
	}
	// The migration itself, so the test proves the file works rather than a
	// copy of it that has drifted.
	body, err := os.ReadFile("../store/migrations/0021_sql_client.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	clk := newClock()
	return NewStore(db, "srv1", fakeKeys{}, clk.now), db, clk
}

func TestConnectionRoundTrip(t *testing.T) {
	s, db, _ := testStore(t)
	ctx := context.Background()

	created, err := s.CreateConnection(ctx, SavedConnection{
		Name: "reporting", Engine: "postgres", Host: "10.0.0.5", Port: 5432,
		Username: "readonly", Database: "app", TLS: TLSRequire, ReadOnly: true,
	}, "s3cr3t", "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := s.Connections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "reporting" || !list[0].ReadOnly {
		t.Fatalf("list = %+v", list)
	}

	// The password is not in the struct at all, so it cannot be marshalled by
	// accident. What is in the column is not the password either.
	var stored []byte
	if err := db.QueryRow(`SELECT password_enc FROM sql_connections WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "s3cr3t") {
		t.Fatal("the password is in the database in the clear")
	}

	cfg, meta, err := s.ConnectionConfig(ctx, created.ID)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Password != "s3cr3t" {
		t.Errorf("decrypted password = %q", cfg.Password)
	}
	if !cfg.ReadOnly || meta.Name != "reporting" {
		t.Errorf("config = %+v, meta = %+v", cfg, meta)
	}

	// An edit without a password leaves the stored one alone.
	if err := s.UpdateConnection(ctx, created.ID, SavedConnection{
		Name: "reporting", Engine: "postgres", Host: "10.0.0.6", Port: 5432,
		Username: "readonly", Database: "app", TLS: TLSRequire, ReadOnly: true,
	}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	cfg, _, err = s.ConnectionConfig(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "10.0.0.6" || cfg.Password != "s3cr3t" {
		t.Errorf("after an edit with no password: %+v", cfg)
	}

	if err := s.DeleteConnection(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connection(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
}

func TestConnectionValidation(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   SavedConnection
		want string
	}{
		{"no name", SavedConnection{Engine: "postgres", Host: "h", Username: "u"}, "name"},
		{"no host", SavedConnection{Name: "n", Engine: "postgres", Username: "u"}, "host"},
		{"unsupported engine", SavedConnection{Name: "n", Engine: "mongodb", Host: "h", Username: "u"}, "Adminer"},
		{"bad tls mode", SavedConnection{Name: "n", Engine: "postgres", Host: "h", Username: "u", TLS: "maybe"}, "TLS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateConnection(ctx, tc.in, "p", "alice"); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}

	// A missing port is filled in per engine rather than refused.
	c := SavedConnection{Name: "mine", Engine: "mysql", Host: "h", Username: "u"}
	got, err := s.CreateConnection(ctx, c, "p", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 3306 {
		t.Errorf("port = %d, want the MySQL default", got.Port)
	}
}

func TestRefusesToStoreASecretWithNoKeyStore(t *testing.T) {
	s, _, _ := testStore(t)
	s.keys = fakeKeys{fail: true}
	_, err := s.CreateConnection(context.Background(), SavedConnection{
		Name: "x", Engine: "postgres", Host: "h", Username: "u",
	}, "p", "alice")
	if err == nil {
		t.Fatal("a connection was stored although its password could not be encrypted")
	}
}

func TestSavedQueryTakesItsParametersFromTheSQL(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()

	q, err := s.SaveQuery(ctx, SavedQuery{
		Name: "recent orders",
		SQL:  "SELECT * FROM orders WHERE customer = :customer AND created_at > :since;\nSELECT :customer",
		// A client claiming other parameters is ignored: the text decides.
		Params: []string{"nonsense"},
	}, "postgres", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(q.Params, ",") != "customer,since" {
		t.Errorf("params = %v, want them read out of the SQL in order", q.Params)
	}

	list, err := s.SavedQueries(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if err := s.DeleteQuery(ctx, q.ID); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRecordsFailuresToo(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()

	entries := []HistoryEntry{
		{ConnectionRef: "pg", SQL: "SELECT 1", Actor: "alice", Kind: "read", RowCount: 1, DurationMS: 3},
		{ConnectionRef: "pg", SQL: "SELECT nope", Actor: "alice", Kind: "read", Error: "column does not exist"},
		{ConnectionRef: "my", SQL: "UPDATE t SET a=1 WHERE id=2", Actor: "bob", Kind: "write", RowCount: 1},
	}
	for _, e := range entries {
		if err := s.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.History(ctx, HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d entries, want 3 including the failure", len(all))
	}
	if all[0].SQL != "UPDATE t SET a=1 WHERE id=2" {
		t.Errorf("history should be newest first, got %q", all[0].SQL)
	}

	mine, err := s.History(ctx, HistoryFilter{Actor: "bob"})
	if err != nil || len(mine) != 1 {
		t.Fatalf("filter by actor: %+v %v", mine, err)
	}
	found, err := s.History(ctx, HistoryFilter{Search: "nope"})
	if err != nil || len(found) != 1 {
		t.Fatalf("search: %+v %v", found, err)
	}
	byConn, err := s.History(ctx, HistoryFilter{ConnectionRef: "my"})
	if err != nil || len(byConn) != 1 {
		t.Fatalf("filter by connection: %+v %v", byConn, err)
	}
}

func TestHistorySearchTreatsWildcardsAsText(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, HistoryEntry{ConnectionRef: "pg", SQL: "SELECT 1", Actor: "alice"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.History(ctx, HistoryFilter{Search: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a literal %% matched %d rows; the search box is not a LIKE pattern", len(got))
	}
}

func TestHistoryIsPrunedByAge(t *testing.T) {
	s, db, clk := testStore(t)
	ctx := context.Background()

	old := clk.now().AddDate(0, 0, -HistoryKeepDays-1).UTC().Format(stampFormat)
	for i := 0; i < 3; i++ {
		if err := s.Record(ctx, HistoryEntry{ConnectionRef: "pg", SQL: "SELECT old", Actor: "alice", StartedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Record(ctx, HistoryEntry{ConnectionRef: "pg", SQL: "SELECT new", Actor: "alice"}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.PruneHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("pruned %d rows, want the 3 that were older than %d days", removed, HistoryKeepDays)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sql_history`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows left, want 1", n)
	}
}

func TestPruneKeepsRecentRows(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.Record(ctx, HistoryEntry{ConnectionRef: "pg", SQL: "SELECT 1", Actor: "alice"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PruneHistory(ctx); err != nil {
		t.Fatal(err)
	}
	left, err := s.History(ctx, HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 5 {
		t.Errorf("%d rows left, want all 5: they are neither old nor beyond the row limit", len(left))
	}
}

func TestStoreTimestampsAreUTC(t *testing.T) {
	s, _, clk := testStore(t)
	got := s.stamp()
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("timestamp %q is not UTC", got)
	}
	if want := clk.now().UTC().Format(time.RFC3339); got[:19] != want[:19] {
		t.Errorf("timestamp %q does not match the clock %q", got, want)
	}
}
