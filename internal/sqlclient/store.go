package sqlclient

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The three tables from section 4, and nothing else. Saved connections, saved
// queries and history are rows rather than a subsystem because the panel
// already has migrations, an encrypted key store and a server id to hang them
// on.

// Keys is the part of the panel's key store this package needs. Taking an
// interface keeps the package testable and keeps auth out of its imports.
type Keys interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(blob []byte) ([]byte, error)
}

// Store reads and writes the SQL client's own tables.
type Store struct {
	db       *sql.DB
	serverID string
	keys     Keys
	now      func() time.Time
}

// NewStore builds a store over the panel's SQLite database.
func NewStore(db *sql.DB, serverID string, keys Keys, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, serverID: serverID, keys: keys, now: now}
}

// SavedConnection is an external database somebody added by hand.
//
// There is no password field, in this struct or in the JSON. A password is
// written through CreateConnection or SetPassword and read only by
// ConnectionConfig, which hands it straight to a driver. It is never returned
// to the browser, not even masked, because a masked secret is still a secret
// that travelled.
type SavedConnection struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Engine    string  `json:"engine"`
	Host      string  `json:"host"`
	Port      int     `json:"port"`
	Username  string  `json:"username"`
	Database  string  `json:"database"`
	TLS       TLSMode `json:"tls"`
	ReadOnly  bool    `json:"readOnly"`
	CreatedBy string  `json:"createdBy"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

// Valid checks a connection before it is stored, so a bad row cannot be
// written and then fail mysteriously at connect time.
func (c *SavedConnection) Valid() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Host = strings.TrimSpace(c.Host)
	switch {
	case c.Name == "":
		return errors.New("give the connection a name")
	case c.Host == "":
		return errors.New("give the connection a host")
	case c.Username == "":
		return errors.New("give the connection a username")
	}
	switch strings.ToLower(c.Engine) {
	case "postgres", "mysql", "mariadb":
		c.Engine = strings.ToLower(c.Engine)
	default:
		return fmt.Errorf("%q is not an engine this client can open: use postgres or mysql, and Adminer for anything else", c.Engine)
	}
	switch c.TLS {
	case TLSDisable, TLSRequire, TLSVerifyFull:
	case "":
		c.TLS = TLSDisable
	default:
		return fmt.Errorf("%q is not a TLS mode", c.TLS)
	}
	if c.Port <= 0 || c.Port > 65535 {
		if dialectOf(c.Engine) == MySQL {
			c.Port = 3306
		} else {
			c.Port = 5432
		}
	}
	return nil
}

const connectionColumns = `id, name, engine, host, port, username, database, tls_mode, read_only, created_by, created_at, updated_at`

// Connections lists the saved external connections, without their passwords.
func (s *Store) Connections(ctx context.Context) ([]SavedConnection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+connectionColumns+` FROM sql_connections WHERE server_id = ? ORDER BY name`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedConnection{}
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Connection reads one saved connection, without its password.
func (s *Store) Connection(ctx context.Context, id string) (*SavedConnection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM sql_connections WHERE server_id = ? AND id = ?`, s.serverID, id)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

type scanner interface{ Scan(dest ...any) error }

func scanConnection(r scanner) (*SavedConnection, error) {
	var c SavedConnection
	var readOnly int
	err := r.Scan(&c.ID, &c.Name, &c.Engine, &c.Host, &c.Port, &c.Username, &c.Database,
		&c.TLS, &readOnly, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.ReadOnly = readOnly == 1
	return &c, nil
}

// CreateConnection stores a connection and its encrypted password.
func (s *Store) CreateConnection(ctx context.Context, c SavedConnection, password, actor string) (*SavedConnection, error) {
	if err := c.Valid(); err != nil {
		return nil, err
	}
	enc, err := s.encrypt(password)
	if err != nil {
		return nil, err
	}
	c.ID = newID()
	c.CreatedBy = actor
	now := s.stamp()
	_, err = s.db.ExecContext(ctx, `INSERT INTO sql_connections
		(id, server_id, name, engine, host, port, username, database, password_enc, tls_mode, read_only, created_by, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, s.serverID, c.Name, c.Engine, c.Host, c.Port, c.Username, c.Database, enc,
		string(c.TLS), boolToInt(c.ReadOnly), actor, now, now)
	if err != nil {
		return nil, storeErr(err, c.Name)
	}
	c.CreatedAt, c.UpdatedAt = now, now
	return &c, nil
}

// UpdateConnection replaces the editable fields. A nil password leaves the
// stored one alone, which is how an edit form that never received the secret
// can still save.
func (s *Store) UpdateConnection(ctx context.Context, id string, c SavedConnection, password *string) error {
	if err := c.Valid(); err != nil {
		return err
	}
	now := s.stamp()
	res, err := s.db.ExecContext(ctx, `UPDATE sql_connections
		SET name = ?, engine = ?, host = ?, port = ?, username = ?, database = ?,
		    tls_mode = ?, read_only = ?, updated_at = ?
		WHERE server_id = ? AND id = ?`,
		c.Name, c.Engine, c.Host, c.Port, c.Username, c.Database,
		string(c.TLS), boolToInt(c.ReadOnly), now, s.serverID, id)
	if err != nil {
		return storeErr(err, c.Name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if password != nil {
		return s.SetPassword(ctx, id, *password)
	}
	return nil
}

// SetPassword replaces the stored secret.
func (s *Store) SetPassword(ctx context.Context, id, password string) error {
	enc, err := s.encrypt(password)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE sql_connections SET password_enc = ?, updated_at = ? WHERE server_id = ? AND id = ?`,
		enc, s.stamp(), s.serverID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteConnection removes a saved connection.
func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sql_connections WHERE server_id = ? AND id = ?`, s.serverID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ConnectionConfig assembles what a driver needs, decrypting the password on
// the way past. This is the only path the secret takes, and it goes from the
// database to a driver without passing through anything that marshals.
func (s *Store) ConnectionConfig(ctx context.Context, id string) (Config, *SavedConnection, error) {
	c, err := s.Connection(ctx, id)
	if err != nil {
		return Config{}, nil, err
	}
	var enc []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT password_enc FROM sql_connections WHERE server_id = ? AND id = ?`, s.serverID, id).Scan(&enc); err != nil {
		return Config{}, nil, err
	}
	password, err := s.decrypt(enc)
	if err != nil {
		return Config{}, nil, fmt.Errorf("the stored password for %q could not be decrypted: %w", c.Name, err)
	}
	return Config{
		Engine: c.Engine, Host: c.Host, Port: c.Port, User: c.Username,
		Password: password, Database: c.Database, TLS: c.TLS, ReadOnly: c.ReadOnly,
	}, c, nil
}

// SavedQuery is a named statement, optionally bound to a connection.
type SavedQuery struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	SQL           string   `json:"sql"`
	ConnectionRef string   `json:"connectionRef,omitempty"`
	Params        []string `json:"params"`
	CreatedBy     string   `json:"createdBy"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

// SavedQueries lists them, newest name order.
func (s *Store) SavedQueries(ctx context.Context) ([]SavedQuery, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, sql, connection_ref, params_json, created_by, created_at, updated_at
		 FROM sql_saved_queries WHERE server_id = ? ORDER BY name`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedQuery{}
	for rows.Next() {
		var q SavedQuery
		var params string
		if err := rows.Scan(&q.ID, &q.Name, &q.Description, &q.SQL, &q.ConnectionRef, &params,
			&q.CreatedBy, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(params), &q.Params)
		if q.Params == nil {
			q.Params = []string{}
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// SaveQuery stores a new saved query. The declared parameters are taken from
// the SQL itself rather than trusted from the client, so they cannot disagree.
func (s *Store) SaveQuery(ctx context.Context, q SavedQuery, engine, actor string) (*SavedQuery, error) {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		return nil, errors.New("give the query a name")
	}
	if strings.TrimSpace(q.SQL) == "" {
		return nil, errors.New("the query is empty")
	}
	q.Params = declaredParams(q.SQL, engine)
	params, _ := json.Marshal(q.Params)
	q.ID = newID()
	q.CreatedBy = actor
	now := s.stamp()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sql_saved_queries
		(id, server_id, name, description, sql, connection_ref, params_json, created_by, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		q.ID, s.serverID, q.Name, q.Description, q.SQL, q.ConnectionRef, string(params), actor, now, now)
	if err != nil {
		return nil, storeErr(err, q.Name)
	}
	q.CreatedAt, q.UpdatedAt = now, now
	return &q, nil
}

// UpdateQuery replaces a saved query.
func (s *Store) UpdateQuery(ctx context.Context, id string, q SavedQuery, engine string) error {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		return errors.New("give the query a name")
	}
	params, _ := json.Marshal(declaredParams(q.SQL, engine))
	res, err := s.db.ExecContext(ctx, `UPDATE sql_saved_queries
		SET name = ?, description = ?, sql = ?, connection_ref = ?, params_json = ?, updated_at = ?
		WHERE server_id = ? AND id = ?`,
		q.Name, q.Description, q.SQL, q.ConnectionRef, string(params), s.stamp(), s.serverID, id)
	if err != nil {
		return storeErr(err, q.Name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteQuery removes a saved query.
func (s *Store) DeleteQuery(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sql_saved_queries WHERE server_id = ? AND id = ?`, s.serverID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// declaredParams collects the :name placeholders across every statement in the
// text, in order of first appearance.
func declaredParams(text, engine string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, st := range Split(text, engine) {
		for _, p := range st.Params {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// HistoryEntry is one executed statement.
type HistoryEntry struct {
	ID            int64  `json:"id"`
	ConnectionRef string `json:"connectionRef"`
	SQL           string `json:"sql"`
	Actor         string `json:"actor"`
	Kind          string `json:"kind"`
	StartedAt     string `json:"startedAt"`
	DurationMS    int64  `json:"durationMs"`
	RowCount      int64  `json:"rowCount"`
	Truncated     bool   `json:"truncated"`
	Error         string `json:"error,omitempty"`
}

// Record writes one statement to the history. It is called for every statement
// that actually ran, including the ones that failed: a history that only keeps
// the successes is no use at three in the morning.
func (s *Store) Record(ctx context.Context, e HistoryEntry) error {
	if e.StartedAt == "" {
		e.StartedAt = s.stamp()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sql_history
		(server_id, connection_ref, sql, actor, kind, started_at, duration_ms, row_count, truncated, error)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		s.serverID, e.ConnectionRef, e.SQL, e.Actor, e.Kind, e.StartedAt,
		e.DurationMS, e.RowCount, boolToInt(e.Truncated), e.Error)
	return err
}

// HistoryFilter narrows the history list.
type HistoryFilter struct {
	ConnectionRef string
	Actor         string
	Search        string
	Limit         int
}

// History lists recent statements, newest first.
func (s *Store) History(ctx context.Context, f HistoryFilter) ([]HistoryEntry, error) {
	q := `SELECT id, connection_ref, sql, actor, kind, started_at, duration_ms, row_count, truncated, error
	      FROM sql_history WHERE server_id = ?`
	args := []any{s.serverID}
	if f.ConnectionRef != "" {
		q += ` AND connection_ref = ?`
		args = append(args, f.ConnectionRef)
	}
	if f.Actor != "" {
		q += ` AND actor = ?`
		args = append(args, f.Actor)
	}
	if f.Search != "" {
		q += ` AND sql LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Search)+"%")
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		var truncated int
		if err := rows.Scan(&e.ID, &e.ConnectionRef, &e.SQL, &e.Actor, &e.Kind, &e.StartedAt,
			&e.DurationMS, &e.RowCount, &truncated, &e.Error); err != nil {
			return nil, err
		}
		e.Truncated = truncated == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClearHistory empties the history for this server.
func (s *Store) ClearHistory(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sql_history WHERE server_id = ?`, s.serverID)
	return err
}

// PruneHistory keeps the last HistoryKeepRows rows or HistoryKeepDays days,
// whichever is smaller, and reports how many rows it removed. A history table
// that grows forever is a bug, not a feature.
func (s *Store) PruneHistory(ctx context.Context) (int64, error) {
	cutoff := s.now().UTC().AddDate(0, 0, -HistoryKeepDays).Format(stampFormat)
	res, err := s.db.ExecContext(ctx, `DELETE FROM sql_history
		WHERE server_id = ?
		  AND (started_at < ?
		       OR id NOT IN (SELECT id FROM sql_history WHERE server_id = ? ORDER BY id DESC LIMIT ?))`,
		s.serverID, cutoff, s.serverID, HistoryKeepRows)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

const stampFormat = "2006-01-02T15:04:05.000Z"

func (s *Store) stamp() string { return s.now().UTC().Format(stampFormat) }

func (s *Store) encrypt(secret string) ([]byte, error) {
	if s.keys == nil {
		return nil, errors.New("no key store: refusing to write a password in the clear")
	}
	return s.keys.Encrypt([]byte(secret))
}

func (s *Store) decrypt(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	if s.keys == nil {
		return "", errors.New("no key store")
	}
	b, err := s.keys.Decrypt(blob)
	return string(b), err
}

func storeErr(err error, name string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("there is already something called %q", name)
	}
	return err
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sqlclient: no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
