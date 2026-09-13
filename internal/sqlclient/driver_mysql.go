package sqlclient

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The MySQL and MariaDB driver. go-sql-driver through database/sql, which is
// enough here because MySQL's protocol does not carry the typed field
// descriptions pgx gives us; the column type names come from the result
// metadata instead. Pure Go, so the build stays at CGO_ENABLED=0.
//
// The one thing database/sql cannot do is cancel: closing the connection
// leaves the query running on the server. So a cancel opens a second
// connection and issues KILL QUERY against the first one's id, which is what
// the mysql command line client does.

func init() { RegisterDriver("mysql", myDriver{}) }

type myDriver struct{}

func (myDriver) Open(ctx context.Context, cfg Config) (Conn, error) {
	db, err := sql.Open("mysql", mysqlDSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("connection settings are not usable: %w", err)
	}
	// One connection per Conn: the pool above this is the pool.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(PoolMaxUnused)

	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, reachError(cfg, err)
	}

	c := &myConn{db: db, conn: conn, cfg: cfg}
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&c.connID); err != nil {
		_ = c.Close(ctx)
		return nil, reachError(cfg, err)
	}
	if cfg.ReadOnly {
		// Belt and braces, as in Postgres: the classifier refuses writes
		// before they are sent and the server refuses them after.
		if _, err := conn.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY"); err != nil {
			_ = c.Close(ctx)
			return nil, myError(err)
		}
	}
	return c, nil
}

func mysqlDSN(cfg Config) string {
	m := mysql.NewConfig()
	m.User = cfg.User
	m.Passwd = cfg.Password
	m.Net = "tcp"
	m.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(port(cfg, 3306)))
	m.DBName = cfg.Database
	m.Timeout = dialTimeout
	m.ParseTime = true // DATETIME arrives as time.Time, not as bytes to guess at
	m.InterpolateParams = false
	// MySQL has no program_name. An unknown DSN parameter is sent as
	// "SET <name>=<value>" at connect, unquoted, so putting one here is a
	// syntax error on the first statement of every connection; the way to
	// name yourself in SHOW PROCESSLIST is a connection attribute.
	m.ConnectionAttributes = "program_name:" + appName(cfg)
	switch cfg.TLS {
	case TLSVerifyFull:
		m.TLSConfig = "true"
	case TLSRequire:
		m.TLSConfig = "skip-verify"
	default:
		m.TLSConfig = "false"
	}
	return m.FormatDSN()
}

type myConn struct {
	db     *sql.DB
	conn   *sql.Conn
	cfg    Config
	connID int64
}

func (c *myConn) Ping(ctx context.Context) error { return c.conn.PingContext(ctx) }

func (c *myConn) Version(ctx context.Context) (string, error) {
	var v string
	err := c.conn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v)
	return v, myError(err)
}

func (c *myConn) Run(ctx context.Context, query string, args []any, wantRows bool) (Cursor, error) {
	if !wantRows {
		// Asking for rows from an INSERT here throws away the affected count,
		// which is the only thing that statement has to report.
		res, err := c.conn.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, myError(err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			n = -1
		}
		return &myCursor{affected: n}, nil
	}
	rows, err := c.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, myError(err)
	}
	cur, err := newMyCursor(rows, c.cfg.Engine)
	if err != nil {
		rows.Close()
		return nil, myError(err)
	}
	return cur, nil
}

// Cancel opens a second connection and kills the query running on this one.
// Closing this connection instead would return control to the user while the
// query kept running and kept its locks.
func (c *myConn) Cancel(ctx context.Context) error {
	side, err := sql.Open("mysql", mysqlDSN(c.cfg))
	if err != nil {
		return err
	}
	defer side.Close()
	side.SetMaxOpenConns(1)
	kctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The id is this connection's own, taken at open time, so there is no
	// window in which it could name somebody else's query.
	if _, err := side.ExecContext(kctx, "KILL QUERY "+strconv.FormatInt(c.connID, 10)); err != nil {
		return myError(err)
	}
	return nil
}

func (c *myConn) Close(context.Context) error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return c.db.Close()
}

type myCursor struct {
	rows     *sql.Rows
	cols     []Column
	dest     []any
	holders  []any
	affected int64
	err      error
	closed   bool
}

func newMyCursor(rows *sql.Rows, engine string) (*myCursor, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	cols := make([]Column, len(types))
	for i, t := range types {
		name := t.DatabaseTypeName()
		cols[i] = Column{Name: t.Name(), Type: name, Class: classFor(engine, name)}
	}
	c := &myCursor{rows: rows, cols: cols, affected: -1}
	c.dest = make([]any, len(cols))
	c.holders = make([]any, len(cols))
	for i := range c.dest {
		c.holders[i] = &c.dest[i]
	}
	return c, nil
}

func (m *myCursor) Columns() []Column { return m.cols }

func (m *myCursor) Next() bool {
	if m.rows == nil {
		return false
	}
	return m.rows.Next()
}

func (m *myCursor) Values() ([]Value, error) {
	if err := m.rows.Scan(m.holders...); err != nil {
		return nil, myError(err)
	}
	out := make([]Value, len(m.dest))
	for i, v := range m.dest {
		out[i] = encode(v, m.cols[i].Class)
	}
	return out, nil
}

func (m *myCursor) Err() error {
	if m.err != nil {
		return m.err
	}
	if m.rows == nil {
		return nil
	}
	return myError(m.rows.Err())
}

func (m *myCursor) RowsAffected() int64 { return m.affected }

func (m *myCursor) Close() {
	if m.closed || m.rows == nil {
		return
	}
	m.closed = true
	_ = m.rows.Close()
}

func myError(err error) error {
	if err == nil {
		return nil
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return &QueryError{
			Message: me.Message,
			Code:    strconv.FormatUint(uint64(me.Number), 10),
			// MySQL does not report a position, so the editor underlines the
			// whole statement rather than pretending to know better.
		}
	}
	return err
}
