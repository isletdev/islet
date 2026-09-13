package sqlclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// The Postgres driver. pgx in its native mode, not through database/sql,
// because that is where the typed results, the field descriptions and a real
// cancel request live. It is pure Go, so the build stays at CGO_ENABLED=0.

func init() { RegisterDriver("postgres", pgDriver{}) }

type pgDriver struct{}

func (pgDriver) Open(ctx context.Context, cfg Config) (Conn, error) {
	pc, err := pgx.ParseConfig(postgresDSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("connection settings are not usable: %w", err)
	}
	pc.RuntimeParams["application_name"] = appName(cfg)
	// A server-side ceiling as well as the per-statement context. If the
	// daemon is killed mid-query the database still lets go.
	pc.RuntimeParams["statement_timeout"] = strconv.FormatInt(MaxTimeout.Milliseconds(), 10)
	pc.RuntimeParams["idle_in_transaction_session_timeout"] = strconv.FormatInt(SessionIdleTimeout.Milliseconds(), 10)
	if cfg.ReadOnly {
		// Belt and braces: the classifier refuses writes before they are sent,
		// and the server refuses them if anything ever gets past it.
		pc.RuntimeParams["default_transaction_read_only"] = "on"
	}

	conn, err := pgx.ConnectConfig(ctx, pc)
	if err != nil {
		return nil, reachError(cfg, err)
	}
	return &pgConn{conn: conn, engine: cfg.Engine}, nil
}

func postgresDSN(cfg Config) string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(port(cfg, 5432))),
		Path:   "/" + cfg.Database,
	}
	q := url.Values{}
	switch cfg.TLS {
	case TLSVerifyFull:
		q.Set("sslmode", "verify-full")
	case TLSRequire:
		q.Set("sslmode", "require")
	default:
		// A container on the host's own bridge network is not carrying its
		// traffic anywhere a TLS session would protect it.
		q.Set("sslmode", "disable")
	}
	q.Set("connect_timeout", "10")
	u.RawQuery = q.Encode()
	return u.String()
}

type pgConn struct {
	conn   *pgx.Conn
	engine string
}

func (c *pgConn) Ping(ctx context.Context) error { return c.conn.Ping(ctx) }

func (c *pgConn) Version(ctx context.Context) (string, error) {
	var v string
	err := c.conn.QueryRow(ctx, "SELECT version()").Scan(&v)
	return v, pgError(err)
}

func (c *pgConn) Run(ctx context.Context, sql string, args []any, _ bool) (Cursor, error) {
	// Postgres reports what a statement returned; it does not have to be told
	// in advance, so wantRows is ignored here and honoured in MySQL.
	rows, err := c.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, pgError(err)
	}
	return newPGCursor(rows, c.conn.TypeMap(), c.engine), nil
}

func (c *pgConn) Cancel(ctx context.Context) error {
	// A cancel request is a second, separate connection to the postmaster.
	// This is the part a client that only abandons the response never does.
	return c.conn.PgConn().CancelRequest(ctx)
}

func (c *pgConn) Close(ctx context.Context) error { return c.conn.Close(ctx) }

type pgCursor struct {
	rows   pgx.Rows
	cols   []Column
	closed bool
	tag    pgconn.CommandTag
}

func newPGCursor(rows pgx.Rows, m *pgtype.Map, engine string) *pgCursor {
	fds := rows.FieldDescriptions()
	cols := make([]Column, len(fds))
	for i, f := range fds {
		name := pgTypeName(m, f.DataTypeOID)
		cols[i] = Column{Name: f.Name, Type: name, Class: classFor(engine, name)}
	}
	return &pgCursor{rows: rows, cols: cols}
}

func pgTypeName(m *pgtype.Map, oid uint32) string {
	if t, ok := m.TypeForOID(oid); ok {
		return t.Name
	}
	return "oid" + strconv.FormatUint(uint64(oid), 10)
}

func (p *pgCursor) Columns() []Column { return p.cols }

func (p *pgCursor) Next() bool { return p.rows.Next() }

func (p *pgCursor) Values() ([]Value, error) {
	vals, err := p.rows.Values()
	if err != nil {
		return nil, pgError(err)
	}
	out := make([]Value, len(vals))
	for i, v := range vals {
		class := ClassUnknown
		if i < len(p.cols) {
			class = p.cols[i].Class
		}
		out[i] = encode(v, class)
	}
	return out, nil
}

func (p *pgCursor) Err() error { return pgError(p.rows.Err()) }

// RowsAffected closes the result set, because the command tag is only
// available once it is closed, and a truncated read is closed early on purpose.
func (p *pgCursor) RowsAffected() int64 {
	p.Close()
	return p.tag.RowsAffected()
}

func (p *pgCursor) Close() {
	if p.closed {
		return
	}
	p.closed = true
	p.rows.Close()
	p.tag = p.rows.CommandTag()
}

// pgError turns a Postgres error into one the editor can point at.
func pgError(err error) error {
	if err == nil {
		return nil
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return &QueryError{
			Message:  pe.Message,
			Code:     pe.Code,
			Detail:   pe.Detail,
			Hint:     pe.Hint,
			Position: int(pe.Position),
		}
	}
	return err
}

// reachError explains a failure to connect in terms of the thing the user can
// change, rather than handing them a dial error and letting them guess.
func reachError(cfg Config, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("nothing is listening on %s:%d. If this is a database Islet installed, the container may be stopped; if it was recreated, its address has changed and will be picked up on the next refresh", cfg.Host, port(cfg, 5432))
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "context deadline exceeded"):
		return fmt.Errorf("could not reach %s:%d within 10 seconds. The host may be firewalled, or the container may be on a network this server cannot see", cfg.Host, port(cfg, 5432))
	case strings.Contains(msg, "password authentication failed"), strings.Contains(msg, "Access denied"):
		return fmt.Errorf("the database refused the credentials for user %q", cfg.User)
	case strings.Contains(msg, "does not exist"):
		return fmt.Errorf("the database %q does not exist on %s", cfg.Database, cfg.Host)
	}
	return err
}

func port(cfg Config, fallback int) int {
	if cfg.Port > 0 {
		return cfg.Port
	}
	return fallback
}

func appName(cfg Config) string {
	if cfg.AppName != "" {
		return cfg.AppName
	}
	return "islet-sql"
}

// dialTimeout is the ceiling on making a connection, kept short so a wrong
// address fails with an explanation instead of a spinner.
const dialTimeout = 10 * time.Second
