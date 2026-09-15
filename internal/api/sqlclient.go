// SQL client: the HTTP edge. It validates, authorises and marshals, and does
// nothing else. Every decision it makes is made in internal/sqlclient.
//
// Admin only, throughout. Arbitrary SQL is equivalent to root on the data, so
// this is not a matter of hiding buttons: every handler checks.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/db"
	"github.com/isletdev/islet/internal/sqlclient"
	"github.com/isletdev/islet/pkg/api"
)

// sqlActor is who is asking, and whether they may ask at all.
type sqlActor struct {
	Name  string
	Admin bool
}

// sqlInstance is a database Islet installed, reduced to what the client needs.
type sqlInstance struct {
	Name     string
	Engine   string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	State    string
	// PublicPort is the host port the container publishes, when it publishes
	// one. It is the second address the client tries; see Config in
	// internal/sqlclient/driver.go for when the first one cannot work.
	PublicPort int
}

// installedDatabases lists the databases Islet installed, with the root
// credentials the client connects as. Admin-only is what makes that
// acceptable. It is reached through the sqlInstances field so that the tests
// can supply a list without a Docker daemon behind it.
func (s *Server) installedDatabases(ctx context.Context, actor string) ([]sqlInstance, error) {
	if s.db == nil {
		return nil, nil
	}
	list, err := s.db.List(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := make([]sqlInstance, 0, len(list))
	for _, in := range list {
		out = append(out, sqlInstance{
			Name: in.Name, Engine: in.Engine, Host: in.IP, Port: sqlPortFor(in),
			User: sqlUserFor(in), Password: sqlPasswordFor(in),
			Database: in.Database, State: in.State,
			PublicPort: publishedPort(in.Public),
		})
	}
	return out, nil
}

// The root account is the one that can see every database in the instance,
// which is what a schema browser is for. The per-app account is a fallback for
// an instance that never had a root password recorded.
func sqlUserFor(in db.Instance) string {
	if in.RootUser != "" {
		return in.RootUser
	}
	return in.User
}

func sqlPasswordFor(in db.Instance) string {
	if in.RootUser != "" && in.RootPass != "" {
		return in.RootPass
	}
	return in.Password
}

// The port on the container itself, not the published one: the daemon reaches
// the bridge address directly and nothing has to be published to query it.
func sqlPortFor(in db.Instance) int {
	if in.Port > 0 {
		return in.Port
	}
	switch in.Engine {
	case "mysql", "mariadb":
		return 3306
	default:
		return 5432
	}
}

// publishedPort is the host port out of "1.2.3.4:5432", or zero.
func publishedPort(public string) int {
	i := strings.LastIndex(public, ":")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(public[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// registerSQLRoutes wires the SQL client onto the mux. The admin check inside
// each handler is not a duplicate of requireAuth: authentication and
// authorisation are different questions.
// wrap is the panel's requireAuth; the tests pass nil and put a user in the
// context themselves, which is the only way to unit test a handler whose whole
// job is deciding what a given user may do.
func (s *Server) registerSQLRoutes(mux *http.ServeMux, wrap func(http.HandlerFunc) http.HandlerFunc) {
	if wrap == nil {
		wrap = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}
	r := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, wrap(h)) }

	r("GET /api/v1/sql/connections", s.listConnections)
	r("POST /api/v1/sql/connections", s.createConnection)
	r("PUT /api/v1/sql/connections/{id}", s.updateConnection)
	r("DELETE /api/v1/sql/connections/{id}", s.deleteConnection)
	r("POST /api/v1/sql/connections/{ref}/test", s.testConnection)
	r("GET /api/v1/sql/connections/{ref}/schema", s.schema)
	r("GET /api/v1/sql/connections/{ref}/search", s.searchSchema)
	r("GET /api/v1/sql/connections/{ref}/table/{schema}/{table}", s.table)
	r("POST /api/v1/sql/connections/{ref}/query", s.query)
	r("POST /api/v1/sql/connections/{ref}/cancel", s.cancel)
	r("POST /api/v1/sql/connections/{ref}/explain", s.explain)
	r("POST /api/v1/sql/sessions", s.openSession)
	r("DELETE /api/v1/sql/sessions/{id}", s.closeSession)
	r("GET /api/v1/sql/history", s.listHistory)
	r("DELETE /api/v1/sql/history", s.clearHistory)
	r("GET /api/v1/sql/saved", s.listSaved)
	r("POST /api/v1/sql/saved", s.createSaved)
	r("PUT /api/v1/sql/saved/{id}", s.updateSaved)
	r("DELETE /api/v1/sql/saved/{id}", s.deleteSaved)
}

// StartSQLHistoryPrune keeps the history table from growing forever, which is
// the bug every tool with a history eventually has.
func (s *Server) StartSQLHistoryPrune(ctx context.Context) {
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			if n, err := s.sqlStore.PruneHistory(ctx); err == nil && n > 0 && s.log != nil {
				s.log.Debug("sql history pruned", "rows", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// sqlAdmin returns the caller when they may use the SQL client, and writes the
// refusal otherwise.
func (s *Server) sqlAdmin(w http.ResponseWriter, r *http.Request) (sqlActor, bool) {
	u := userFrom(r.Context())
	if u == nil || u.Role != "admin" {
		// 8.4: deployers and viewers do not get arbitrary SQL.
		fail(w, http.StatusForbidden, "forbidden", "only admins can use the SQL client: arbitrary SQL is equivalent to root on the data")
		return sqlActor{}, false
	}
	return sqlActor{Name: u.Username, Admin: true}, true
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.Error{Error: code, Message: msg})
}

// sqlDecode reads a request body. The panel's own decode caps at 64 kB, which
// is generous for a form and mean for a script somebody pasted.
func sqlDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "could not read the request: "+err.Error())
		return false
	}
	return true
}

// sqlErr maps the package's sentinel errors onto status codes, so the
// interface can tell "you may not" from "that is not there" from "it broke".
func sqlErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sqlclient.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, sqlclient.ErrReadOnly):
		fail(w, http.StatusForbidden, "read_only", err.Error())
	case errors.Is(err, sqlclient.ErrNeedsConfirmation):
		fail(w, http.StatusConflict, "needs_confirmation", err.Error())
	case errors.Is(err, sqlclient.ErrBusy):
		fail(w, http.StatusConflict, "busy", err.Error())
	default:
		fail(w, http.StatusBadRequest, "sql", err.Error())
	}
}

// ---- connections -------------------------------------------------------

// sqlConnectionView is one entry in the connection list. Islet's own databases
// and saved external ones look the same to the client apart from Managed,
// which is what lets the interface say where a connection came from.
type sqlConnectionView struct {
	Ref      string `json:"ref"`
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Managed  bool   `json:"managed"` // installed by Islet: no configuration, no stored password
	ReadOnly bool   `json:"readOnly"`
	State    string `json:"state,omitempty"`
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	out := []sqlConnectionView{}

	instances, err := s.sqlInstances(r.Context(), a.Name)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	for _, in := range instances {
		if _, err := sqlclient.EngineSupported(in.Engine); err != nil {
			continue // Redis and Mongo are Adminer's job; see the non-goals
		}
		out = append(out, sqlConnectionView{
			Ref: "islet:" + in.Name, Name: in.Name, Engine: in.Engine,
			Host: in.Host, Port: in.Port, Database: in.Database,
			Managed: true, State: in.State,
		})
	}

	saved, err := s.sqlStore.Connections(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	for _, c := range saved {
		out = append(out, sqlConnectionView{
			Ref: c.ID, Name: c.Name, Engine: c.Engine, Host: c.Host, Port: c.Port,
			Database: c.Database, ReadOnly: c.ReadOnly,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// connectionRequest is the add and edit form. The password is write-only:
// it comes in and is never sent back.
type connectionRequest struct {
	Name     string  `json:"name"`
	Engine   string  `json:"engine"`
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	Username string  `json:"username"`
	Password *string `json:"password"`
	Database string  `json:"database"`
	TLS      string  `json:"tls"`
	ReadOnly bool    `json:"readOnly"`
}

func (req connectionRequest) toSaved() sqlclient.SavedConnection {
	return sqlclient.SavedConnection{
		Name: req.Name, Engine: req.Engine, Host: req.Host, Port: req.Port,
		Username: req.Username, Database: req.Database,
		TLS: sqlclient.TLSMode(req.TLS), ReadOnly: req.ReadOnly,
	}
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req connectionRequest
	if !sqlDecode(w, r, &req) {
		return
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}

	// 7.1: the form tests before it saves, so a connection that cannot work
	// never becomes a row somebody has to debug later.
	cfg := sqlclient.Config{
		Engine: req.Engine, Host: req.Host, Port: req.Port, User: req.Username,
		Password: password, Database: req.Database, TLS: sqlclient.TLSMode(req.TLS),
		ReadOnly: req.ReadOnly,
	}
	probe := "new:" + req.Name
	_, testErr := s.sqlMgr.Test(r.Context(), probe, a.Name, cfg)
	// The probe's pool would otherwise sit on a live connection until the
	// unused timeout, under a reference nothing will ever look up again.
	s.sqlMgr.Forget(probe)
	if testErr != nil {
		fail(w, http.StatusBadRequest, "unreachable", testErr.Error())
		return
	}

	created, err := s.sqlStore.CreateConnection(r.Context(), req.toSaved(), password, a.Name)
	if err != nil {
		sqlErr(w, err)
		return
	}
	s.sqlAudit(r.Context(), a.Name, "sql.connection.create", created.Name, created.Engine+" at "+created.Host)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req connectionRequest
	if !sqlDecode(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if err := s.sqlStore.UpdateConnection(r.Context(), id, req.toSaved(), req.Password); err != nil {
		sqlErr(w, err)
		return
	}
	// The pool is pointing at the old settings, and a pool that outlives its
	// configuration is how an edit appears not to have taken.
	s.sqlMgr.Forget(id)
	s.sqlCache.Invalidate(id)
	s.sqlAudit(r.Context(), a.Name, "sql.connection.update", req.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.sqlStore.DeleteConnection(r.Context(), id); err != nil {
		sqlErr(w, err)
		return
	}
	s.sqlMgr.Forget(id)
	s.sqlCache.Invalidate(id)
	s.sqlAudit(r.Context(), a.Name, "sql.connection.delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, r.PathValue("ref"))
	if err != nil {
		sqlErr(w, err)
		return
	}
	// 8.6: testing uses the stored secret server-side. The browser never had
	// it and does not get it now.
	info, err := s.sqlMgr.Test(r.Context(), tgt.Ref, a.Name, tgt.Config)
	if err != nil {
		fail(w, http.StatusBadRequest, "unreachable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// ---- schema ------------------------------------------------------------

func (s *Server) schema(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, r.PathValue("ref"))
	if err != nil {
		sqlErr(w, err)
		return
	}
	tree, err := s.tree(r.Context(), tgt, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		sqlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (s *Server) searchSchema(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, r.PathValue("ref"))
	if err != nil {
		sqlErr(w, err)
		return
	}
	tree, err := s.tree(r.Context(), tgt, false)
	if err != nil {
		sqlErr(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	matches := tree.Find(r.URL.Query().Get("q"), limit)
	if matches == nil {
		matches = []sqlclient.Match{}
	}
	writeJSON(w, http.StatusOK, matches)
}

func (s *Server) table(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, r.PathValue("ref"))
	if err != nil {
		sqlErr(w, err)
		return
	}
	detail, err := s.sqlMgr.TableDetail(r.Context(), tgt, r.PathValue("schema"), r.PathValue("table"))
	if err != nil {
		sqlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) tree(ctx context.Context, tgt sqlclient.Target, refresh bool) (*sqlclient.Tree, error) {
	return s.sqlCache.Get(ctx, tgt.Ref, tgt.Engine, refresh, func(ctx context.Context) (*sqlclient.Tree, error) {
		return s.sqlMgr.Introspect(ctx, tgt)
	})
}

// ---- running -----------------------------------------------------------

type queryRequest struct {
	SQL             string         `json:"sql"`
	SessionID       string         `json:"sessionId"`
	RunID           string         `json:"runId"`
	RowCap          int            `json:"rowCap"`
	TimeoutMS       int            `json:"timeoutMs"`
	Transaction     bool           `json:"transaction"`
	ContinueOnError bool           `json:"continueOnError"`
	Params          map[string]any `json:"params"`
	Confirmed       bool           `json:"confirmed"`
}

// query runs a document and streams one message per statement, so a script of
// ten reports each as it finishes rather than going quiet for a minute.
func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req queryRequest
	if !sqlDecode(w, r, &req) {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, r.PathValue("ref"))
	if err != nil {
		sqlErr(w, err)
		return
	}

	var sess *sqlclient.Session
	if req.SessionID != "" {
		sess, err = s.sqlMgr.LookupSession(req.SessionID, a.Name)
		if err != nil {
			sqlErr(w, err)
			return
		}
	}

	opt := sqlclient.RunOptions{
		RowCap:          req.RowCap,
		Timeout:         time.Duration(req.TimeoutMS) * time.Millisecond,
		Transaction:     req.Transaction,
		ContinueOnError: req.ContinueOnError,
		Params:          req.Params,
		Confirmed:       req.Confirmed,
	}

	// Refuse before the stream starts, so a refusal is a status code the
	// client can act on rather than an error event inside a 200.
	if _, err := sqlclient.Preflight(req.SQL, tgt, opt); err != nil {
		sqlErr(w, err)
		return
	}

	fl, isFlusher := w.(http.Flusher)
	if !isFlusher {
		fail(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	// no-transform is the part that is not about caching: it tells a CDN or a
	// proxy not to recompress this, and a compressor in front of a stream holds
	// its first kilobyte back — which for a stream is however long the work
	// takes. Traefik's own compressor is told the same thing by content type,
	// in the middleware Islet writes for it.
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	out := bufio.NewWriter(w)

	runID := req.RunID
	if runID == "" {
		runID = sqlclient.NewRunID()
	}
	send := func(event string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	_ = send("run", map[string]string{"runId": runID})

	emit := func(res sqlclient.StatementResult) error {
		// 8.5: every statement to the audit log with the actor, and to the
		// history table, whether it succeeded or not.
		s.record(r.Context(), a.Name, tgt, res)
		return send("statement", res)
	}

	sum, err := s.sqlMgr.Execute(r.Context(), runID, tgt, sess, req.SQL, opt, emit)
	if err != nil && r.Context().Err() == nil {
		_ = send("error", api.Error{Error: "sql", Message: err.Error()})
	}
	if sum.SchemaChanged {
		// 5.3: DDL invalidates the introspection cache.
		s.sqlCache.Invalidate(tgt.Ref)
	}
	_ = send("end", sum)
}

func (s *Server) record(ctx context.Context, actor string, tgt sqlclient.Target, res sqlclient.StatementResult) {
	rows := int64(res.RowCount)
	if res.RowsAffected >= 0 && res.RowCount == 0 {
		rows = res.RowsAffected
	}
	entry := sqlclient.HistoryEntry{
		ConnectionRef: tgt.Ref, SQL: res.SQL, Actor: actor, Kind: string(res.Kind),
		DurationMS: res.DurationMS, RowCount: rows, Truncated: res.Truncated,
	}
	if res.Error != nil {
		entry.Error = res.Error.Message
	}
	// A failure to write history must not fail the query the user ran.
	_ = s.sqlStore.Record(ctx, entry)
	s.sqlAudit(ctx, actor, "sql.run", tgt.Ref, res.SQL)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		RunID string `json:"runId"`
	}
	if !sqlDecode(w, r, &req) {
		return
	}
	if err := s.sqlMgr.Cancel(r.Context(), req.RunID, a.Name); err != nil {
		sqlErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type explainRequest struct {
	SQL       string `json:"sql"`
	SessionID string `json:"sessionId"`
	// Analyze runs the statement. It is a separate click in the interface for
	// that reason, and a separate field here so it cannot happen by default.
	Analyze bool `json:"analyze"`
}

func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req explainRequest
	if !sqlDecode(w, r, &req) {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, r.PathValue("ref"))
	if err != nil {
		sqlErr(w, err)
		return
	}
	var sess *sqlclient.Session
	if req.SessionID != "" {
		if sess, err = s.sqlMgr.LookupSession(req.SessionID, a.Name); err != nil {
			sqlErr(w, err)
			return
		}
	}
	plan, err := s.sqlMgr.Explain(r.Context(), tgt, sess, req.SQL, req.Analyze)
	if err != nil {
		sqlErr(w, err)
		return
	}
	if req.Analyze {
		s.sqlAudit(r.Context(), a.Name, "sql.explain.analyze", tgt.Ref, req.SQL)
	}
	writeJSON(w, http.StatusOK, plan)
}

// ---- sessions ----------------------------------------------------------

func (s *Server) openSession(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if !sqlDecode(w, r, &req) {
		return
	}
	tgt, err := s.sqlTarget(r.Context(), a, req.Ref)
	if err != nil {
		sqlErr(w, err)
		return
	}
	sess, err := s.sqlMgr.OpenSession(r.Context(), tgt.Ref, a.Name, tgt.Config)
	if err != nil {
		sqlErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     sess.ID,
		"ref":    sess.Ref,
		"engine": sess.Engine,
	})
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	if err := s.sqlMgr.CloseSession(r.Context(), r.PathValue("id"), a.Name); err != nil {
		sqlErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- history and saved queries -----------------------------------------

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sqlAdmin(w, r); !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, err := s.sqlStore.History(r.Context(), sqlclient.HistoryFilter{
		ConnectionRef: q.Get("ref"),
		Actor:         q.Get("actor"),
		Search:        q.Get("q"),
		Limit:         limit,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) clearHistory(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	if err := s.sqlStore.ClearHistory(r.Context()); err != nil {
		fail(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.sqlAudit(r.Context(), a.Name, "sql.history.clear", "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSaved(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sqlAdmin(w, r); !ok {
		return
	}
	list, err := s.sqlStore.SavedQueries(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type savedRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	SQL           string `json:"sql"`
	ConnectionRef string `json:"connectionRef"`
	Engine        string `json:"engine"`
}

func (s *Server) createSaved(w http.ResponseWriter, r *http.Request) {
	a, ok := s.sqlAdmin(w, r)
	if !ok {
		return
	}
	var req savedRequest
	if !sqlDecode(w, r, &req) {
		return
	}
	saved, err := s.sqlStore.SaveQuery(r.Context(), sqlclient.SavedQuery{
		Name: req.Name, Description: req.Description, SQL: req.SQL, ConnectionRef: req.ConnectionRef,
	}, engineOr(req.Engine), a.Name)
	if err != nil {
		sqlErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) updateSaved(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sqlAdmin(w, r); !ok {
		return
	}
	var req savedRequest
	if !sqlDecode(w, r, &req) {
		return
	}
	err := s.sqlStore.UpdateQuery(r.Context(), r.PathValue("id"), sqlclient.SavedQuery{
		Name: req.Name, Description: req.Description, SQL: req.SQL, ConnectionRef: req.ConnectionRef,
	}, engineOr(req.Engine))
	if err != nil {
		sqlErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSaved(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sqlAdmin(w, r); !ok {
		return
	}
	if err := s.sqlStore.DeleteQuery(r.Context(), r.PathValue("id")); err != nil {
		sqlErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func engineOr(e string) string {
	if e == "" {
		return "postgres"
	}
	return e
}

// ---- resolving a connection reference ----------------------------------

// target turns a reference from the client into everything a run needs.
//
// A reference is either "islet:<instance>" for a database Islet installed, or
// a saved connection's id. The bridge address is resolved here, at connection
// time, and never cached: a recreated container gets a new one.
func (s *Server) sqlTarget(ctx context.Context, a sqlActor, ref string) (sqlclient.Target, error) {
	if name, ok := strings.CutPrefix(ref, "islet:"); ok {
		instances, err := s.sqlInstances(ctx, a.Name)
		if err != nil {
			return sqlclient.Target{}, err
		}
		for _, in := range instances {
			if in.Name != name {
				continue
			}
			if _, err := sqlclient.EngineSupported(in.Engine); err != nil {
				return sqlclient.Target{}, err
			}
			if in.Host == "" && in.PublicPort == 0 {
				return sqlclient.Target{}, fmt.Errorf("%s has no address to connect to: the container may be stopped", in.Name)
			}
			return sqlclient.Target{
				Ref: ref, User: a.Name, Engine: in.Engine,
				Config: sqlclient.Config{
					Engine: in.Engine, Host: addressOf(in), Port: portOf(in),
					User: in.User, Password: in.Password, Database: in.Database,
					TLS:     sqlclient.TLSDisable,
					AppName: "islet-sql/" + a.Name,
					// Loopback, not the advertised public address: a published
					// port is reachable here without leaving the machine.
					FallbackHost: fallbackHost(in), FallbackPort: in.PublicPort,
				},
			}, nil
		}
		return sqlclient.Target{}, sqlclient.ErrNotFound
	}

	cfg, meta, err := s.sqlStore.ConnectionConfig(ctx, ref)
	if err != nil {
		return sqlclient.Target{}, err
	}
	return sqlclient.Target{
		Ref: ref, User: a.Name, Engine: meta.Engine,
		ReadOnly: meta.ReadOnly,
		Config:   cfg,
	}, nil
}

// sqlAudit records an action. 8.5: statements are audited with the actor, and
// cmdrun.Redact runs over the detail first, because a statement can carry a
// password — CREATE ROLE ... PASSWORD, ALTER USER, a connection string in a
// string literal — and the audit log is readable by anyone who can read it.
func (s *Server) sqlAudit(ctx context.Context, actor, action, target, detail string) {
	_ = s.store.Audit(ctx, actor, action, target, cmdrun.Redact(detail))
}

// A container with no bridge address the daemon can use still has a published
// port, when it has one. These pick the first address to try and the second.

func addressOf(in sqlInstance) string {
	if in.Host != "" {
		return in.Host
	}
	return "127.0.0.1"
}

func portOf(in sqlInstance) int {
	if in.Host != "" {
		return in.Port
	}
	return in.PublicPort
}

func fallbackHost(in sqlInstance) string {
	if in.Host != "" && in.PublicPort > 0 {
		return "127.0.0.1"
	}
	return ""
}
