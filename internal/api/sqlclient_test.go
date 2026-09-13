package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/sqlclient"
	"github.com/isletdev/islet/internal/store"
)

// ---- a database and a driver to talk to ---------------------------------

type keys struct{}

func (keys) Encrypt(b []byte) ([]byte, error) { return append([]byte("ENC:"), b...), nil }
func (keys) Decrypt(b []byte) ([]byte, error) {
	if len(b) < 4 {
		return nil, errors.New("not encrypted")
	}
	return b[4:], nil
}

type stubDriver struct{ rows [][]sqlclient.Value }

func (d stubDriver) Open(context.Context, sqlclient.Config) (sqlclient.Conn, error) {
	return stubConn{rows: d.rows}, nil
}

type stubConn struct{ rows [][]sqlclient.Value }

func (stubConn) Ping(context.Context) error              { return nil }
func (stubConn) Version(context.Context) (string, error) { return "PostgreSQL 17.0 (stub)", nil }
func (stubConn) Cancel(context.Context) error            { return nil }
func (stubConn) Close(context.Context) error             { return nil }
func (c stubConn) Run(_ context.Context, _ string, _ []any, wantRows bool) (sqlclient.Cursor, error) {
	if !wantRows {
		return &stubCursor{affected: 1}, nil
	}
	return &stubCursor{
		cols:     []sqlclient.Column{{Name: "n", Type: "int4", Class: sqlclient.ClassNumber}},
		rows:     c.rows,
		affected: -1,
	}, nil
}

type stubCursor struct {
	cols     []sqlclient.Column
	rows     [][]sqlclient.Value
	i        int
	affected int64
}

func (s *stubCursor) Columns() []sqlclient.Column { return s.cols }
func (s *stubCursor) Next() bool {
	if s.i >= len(s.rows) {
		return false
	}
	s.i++
	return true
}
func (s *stubCursor) Values() ([]sqlclient.Value, error) { return s.rows[s.i-1], nil }
func (s *stubCursor) Err() error                         { return nil }
func (s *stubCursor) RowsAffected() int64                { return s.affected }
func (s *stubCursor) Close()                             {}

// ---- the server under test ----------------------------------------------

type harness struct {
	mux   *http.ServeMux
	db    *sql.DB
	store *sqlclient.Store
	admin bool
	actor string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sqlclient.RegisterDriver("postgres", stubDriver{rows: [][]sqlclient.Value{{int64(1)}, {int64(2)}}})

	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/panel.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE servers (id TEXT PRIMARY KEY, name TEXT, hostname TEXT, is_local INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers VALUES ('srv1','local','local',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id TEXT NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL,
		target TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../store/migrations/0021_sql_client.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatal(err)
	}

	h := &harness{admin: true, actor: "alice"}
	h.store = sqlclient.NewStore(db, "srv1", keys{}, nil)
	h.db = db

	// The panel's own Server, with the fields the SQL client uses and nothing
	// else. Building it by hand rather than through New keeps a unit test out
	// of the catalog, Docker and the recipe engine.
	srv := &Server{
		store:    &store.Store{DB: db, ServerID: "srv1"},
		sqlMgr:   sqlclient.NewManager(nil, nil),
		sqlStore: h.store,
		sqlCache: sqlclient.NewSchemaCache(nil),
		sqlInstances: func(context.Context, string) ([]sqlInstance, error) {
			return []sqlInstance{
				{Name: "pg", Engine: "postgres", Host: "172.18.0.2", Port: 5432,
					User: "postgres", Password: "hunter2", Database: "app", State: "running"},
				{Name: "cache", Engine: "redis", Host: "172.18.0.3", Port: 6379, State: "running"},
			}, nil
		},
	}
	h.mux = http.NewServeMux()
	srv.registerSQLRoutes(h.mux, nil)
	return h
}

// audits reads the panel's audit log, which is where the handlers actually
// write. Asserting on the table rather than on a callback is the difference
// between testing the wiring and testing a stub.
func (h *harness) auditLog(t *testing.T) []string {
	t.Helper()
	rows, err := h.db.Query(`SELECT actor, action, target, detail FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var actor, action, target, detail string
		if err := rows.Scan(&actor, &action, &target, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, actor+" "+action+" "+target+" "+detail)
	}
	return out
}

func (h *harness) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	role := "admin"
	if !h.admin {
		role = "viewer"
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxUser,
		&auth.User{ID: "u1", Username: h.actor, Role: role}))
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

// ---- tests ---------------------------------------------------------------

// Every route, refused for a non-admin. 8.4: arbitrary SQL is equivalent to
// root on the data, so this is the test that must not be allowed to rot as
// routes are added.
func TestNonAdminIsRefusedEverywhere(t *testing.T) {
	h := newHarness(t)
	h.admin = false

	routes := []struct{ method, path, body string }{
		{"GET", "/api/v1/sql/connections", ""},
		{"POST", "/api/v1/sql/connections", `{}`},
		{"PUT", "/api/v1/sql/connections/x", `{}`},
		{"DELETE", "/api/v1/sql/connections/x", ""},
		{"POST", "/api/v1/sql/connections/islet:pg/test", `{}`},
		{"GET", "/api/v1/sql/connections/islet:pg/schema", ""},
		{"GET", "/api/v1/sql/connections/islet:pg/search?q=a", ""},
		{"GET", "/api/v1/sql/connections/islet:pg/table/public/users", ""},
		{"POST", "/api/v1/sql/connections/islet:pg/query", `{"sql":"SELECT 1"}`},
		{"POST", "/api/v1/sql/connections/islet:pg/cancel", `{"runId":"x"}`},
		{"POST", "/api/v1/sql/connections/islet:pg/explain", `{"sql":"SELECT 1"}`},
		{"POST", "/api/v1/sql/sessions", `{"ref":"islet:pg"}`},
		{"DELETE", "/api/v1/sql/sessions/x", ""},
		{"GET", "/api/v1/sql/history", ""},
		{"DELETE", "/api/v1/sql/history", ""},
		{"GET", "/api/v1/sql/saved", ""},
		{"POST", "/api/v1/sql/saved", `{"name":"x","sql":"SELECT 1"}`},
		{"PUT", "/api/v1/sql/saved/x", `{"name":"x","sql":"SELECT 1"}`},
		{"DELETE", "/api/v1/sql/saved/x", ""},
	}
	for _, rt := range routes {
		w := h.do(t, rt.method, rt.path, rt.body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", rt.method, rt.path, w.Code)
		}
	}
}

func TestConnectionListShowsInstalledDatabasesWithNoConfiguration(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "GET", "/api/v1/sql/connections", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var list []sqlConnectionView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d connections, want only the Postgres one: Redis is not this tool's job", len(list))
	}
	if list[0].Ref != "islet:pg" || !list[0].Managed {
		t.Errorf("connection = %+v", list[0])
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatal("a password reached the client")
	}
}

func TestSavedConnectionNeverReturnsItsPassword(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "POST", "/api/v1/sql/connections", `{
		"name":"reporting","engine":"postgres","host":"10.0.0.9","port":5432,
		"username":"ro","password":"s3cr3t","database":"app","tls":"disable",
		"readOnly":true,"environment":"production"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "s3cr3t") {
		t.Fatal("the create response echoed the password back")
	}
	list := h.do(t, "GET", "/api/v1/sql/connections", "")
	if strings.Contains(list.Body.String(), "s3cr3t") {
		t.Fatal("the list response carried the password")
	}
}

func TestQueryStreamsOneMessagePerStatement(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "POST", "/api/v1/sql/connections/islet:pg/query", `{"sql":"SELECT 1;SELECT 2"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type = %q", ct)
	}
	body := w.Body.String()
	if n := strings.Count(body, "event: statement"); n != 2 {
		t.Errorf("got %d statement events, want 2:\n%s", n, body)
	}
	if !strings.Contains(body, "event: end") {
		t.Error("the stream did not end with a summary")
	}
	if !strings.Contains(body, "event: run") {
		t.Error("the stream did not open with a run id, so there is nothing to cancel by")
	}
}

func TestEveryStatementReachesTheAuditLogAndTheHistory(t *testing.T) {
	h := newHarness(t)
	h.do(t, "POST", "/api/v1/sql/connections/islet:pg/query", `{"sql":"SELECT 1;SELECT 2"}`)

	runs := 0
	for _, a := range h.auditLog(t) {
		if strings.Contains(a, "sql.run") {
			runs++
		}
	}
	if runs != 2 {
		t.Errorf("%d statements audited, want 2", runs)
	}

	entries, err := h.store.History(context.Background(), sqlclient.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d history rows, want 2", len(entries))
	}
	if entries[0].Actor != "alice" {
		t.Errorf("history actor = %q", entries[0].Actor)
	}
}

func TestDangerousStatementIsRefusedBeforeTheStreamStarts(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "POST", "/api/v1/sql/connections/islet:pg/query", `{"sql":"DROP TABLE users"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 so the client can show a confirmation: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "needs_confirmation") {
		t.Errorf("body = %s", w.Body)
	}

	// With the confirmation collected, it runs.
	w = h.do(t, "POST", "/api/v1/sql/connections/islet:pg/query", `{"sql":"DROP TABLE users","confirmed":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("confirmed run status %d: %s", w.Code, w.Body)
	}
}

func TestReadOnlyConnectionRefusesAWrite(t *testing.T) {
	h := newHarness(t)
	created := h.do(t, "POST", "/api/v1/sql/connections", `{
		"name":"ro","engine":"postgres","host":"10.0.0.9","port":5432,
		"username":"ro","password":"p","database":"app","tls":"disable",
		"readOnly":true,"environment":"staging"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var conn sqlclient.SavedConnection
	if err := json.Unmarshal(created.Body.Bytes(), &conn); err != nil {
		t.Fatal(err)
	}

	w := h.do(t, "POST", "/api/v1/sql/connections/"+conn.ID+"/query",
		`{"sql":"UPDATE users SET a = 1 WHERE id = 2","confirmed":true}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: read-only is enforced here, not in the interface. %s", w.Code, w.Body)
	}

	ok := h.do(t, "POST", "/api/v1/sql/connections/"+conn.ID+"/query", `{"sql":"SELECT 1"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("a read on a read-only connection: %d %s", ok.Code, ok.Body)
	}
}

func TestProductionConnectionAsksBeforeAnyWrite(t *testing.T) {
	h := newHarness(t)
	created := h.do(t, "POST", "/api/v1/sql/connections", `{
		"name":"prod","engine":"postgres","host":"10.0.0.9","port":5432,
		"username":"app","password":"p","database":"app","tls":"disable",
		"readOnly":false,"environment":"production"}`)
	var conn sqlclient.SavedConnection
	if err := json.Unmarshal(created.Body.Bytes(), &conn); err != nil {
		t.Fatal(err)
	}
	w := h.do(t, "POST", "/api/v1/sql/connections/"+conn.ID+"/query",
		`{"sql":"UPDATE users SET a = 1 WHERE id = 2"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 on a production write: %s", w.Code, w.Body)
	}
}

func TestUnsupportedEngineIsRefusedWithSomewhereToGo(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "GET", "/api/v1/sql/connections/islet:cache/schema", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "Adminer") {
		t.Errorf("the refusal should point at Adminer, got %s", w.Body)
	}
}

func TestUnknownConnectionIsNotFound(t *testing.T) {
	h := newHarness(t)
	if w := h.do(t, "GET", "/api/v1/sql/connections/islet:nope/schema", ""); w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
	if w := h.do(t, "GET", "/api/v1/sql/connections/deadbeef/schema", ""); w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestSessionsOpenAndClose(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "POST", "/api/v1/sql/sessions", `{"ref":"islet:pg"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	// Another user's session is not found rather than forbidden: a session id
	// they were never given should not be confirmed to exist.
	h.actor = "mallory"
	if w := h.do(t, "DELETE", "/api/v1/sql/sessions/"+out.ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("another user closing the session = %d, want 404", w.Code)
	}
	h.actor = "alice"
	if w := h.do(t, "DELETE", "/api/v1/sql/sessions/"+out.ID, ""); w.Code != http.StatusNoContent {
		t.Errorf("close: %d %s", w.Code, w.Body)
	}
}

func TestSavedQueriesRoundTrip(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "POST", "/api/v1/sql/saved",
		`{"name":"by org","sql":"SELECT * FROM t WHERE org = :org","engine":"postgres"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var saved sqlclient.SavedQuery
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Params) != 1 || saved.Params[0] != "org" {
		t.Errorf("params = %v, want the one declared in the SQL", saved.Params)
	}
	if w := h.do(t, "DELETE", "/api/v1/sql/saved/"+saved.ID, ""); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}
}

func TestHistoryCanBeSearchedAndCleared(t *testing.T) {
	h := newHarness(t)
	h.do(t, "POST", "/api/v1/sql/connections/islet:pg/query", `{"sql":"SELECT 1"}`)

	w := h.do(t, "GET", "/api/v1/sql/history?q=SELECT", "")
	var entries []sqlclient.HistoryEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1", len(entries))
	}
	if w := h.do(t, "DELETE", "/api/v1/sql/history", ""); w.Code != http.StatusNoContent {
		t.Fatalf("clear: %d", w.Code)
	}
	w = h.do(t, "GET", "/api/v1/sql/history", "")
	if !strings.Contains(w.Body.String(), "[]") {
		t.Errorf("history should be empty, got %s", w.Body)
	}
}

func TestExplainDoesNotAnalyzeUnlessAsked(t *testing.T) {
	h := newHarness(t)
	w := h.do(t, "POST", "/api/v1/sql/connections/islet:pg/explain", `{"sql":"SELECT 1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var plan sqlclient.Plan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Analyzed {
		t.Error("a plain EXPLAIN must not report itself as analyzed")
	}

	// ANALYZE on a destructive statement runs it, so it needs the same
	// confirmation a run would.
	w = h.do(t, "POST", "/api/v1/sql/connections/islet:pg/explain",
		`{"sql":"DELETE FROM users","analyze":true}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: EXPLAIN ANALYZE of a DELETE deletes the rows. %s", w.Code, w.Body)
	}
}
