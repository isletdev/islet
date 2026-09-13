package sqlclient

import (
	"context"
	"errors"
	"fmt"
)

// TLSMode is how a connection treats transport security.
type TLSMode string

const (
	TLSDisable    TLSMode = "disable"
	TLSRequire    TLSMode = "require"
	TLSVerifyFull TLSMode = "verify-full"
)

// Config is everything needed to open one connection. It carries the password
// in memory only: it is read from the encrypted store on the way past and is
// never marshalled anywhere.
type Config struct {
	Engine   string // postgres | mysql | mariadb
	Host     string
	Port     int
	User     string
	Password string
	Database string
	TLS      TLSMode
	ReadOnly bool   // enforced here, in the daemon, not in the interface
	AppName  string // what the database's own process list will show

	// FallbackHost and FallbackPort are a second address to try when the
	// first does not answer. A database Islet installed is reached on its
	// container's bridge address, which is right on a Linux server where the
	// daemon and Docker share a host, and wrong wherever they do not: Docker
	// Desktop keeps containers in a VM, and a daemon in a container of its own
	// is on another network. When the port is published there is a second way
	// in, and taking it beats telling somebody their own database is
	// unreachable.
	FallbackHost string
	FallbackPort int
}

// withFallback returns the same configuration aimed at the second address.
func (c Config) withFallback() Config {
	c.Host, c.Port = c.FallbackHost, c.FallbackPort
	c.FallbackHost, c.FallbackPort = "", 0
	return c
}

func (c Config) dialect() Dialect { return dialectOf(c.Engine) }

// Driver opens connections for one engine.
type Driver interface {
	// Open connects and returns a usable connection, or an error explaining
	// what failed in words a person can act on.
	Open(ctx context.Context, cfg Config) (Conn, error)
}

// Conn is one connection to a database. Nothing above this interface knows
// which driver is underneath, which is why the pool, the session bookkeeping
// and the execution loop are all testable without a database.
type Conn interface {
	// Ping checks the connection is still usable.
	Ping(ctx context.Context) error
	// Version is the server's own version string, for the connection test.
	Version(ctx context.Context) (string, error)
	// Run executes one statement. wantRows says whether the caller expects a
	// result set, which MySQL needs told and Postgres works out for itself.
	Run(ctx context.Context, sql string, args []any, wantRows bool) (Cursor, error)
	// Cancel stops whatever this connection is currently running, from
	// another goroutine. Postgres sends a cancel request; MySQL opens a
	// second connection and issues KILL QUERY. A cancel that only abandons
	// the response is not a cancel.
	Cancel(ctx context.Context) error
	// Close releases the connection.
	Close(ctx context.Context) error
}

// Cursor streams one statement's rows. It is closed exactly once, by the
// caller, and RowsAffected is meaningful only after it is exhausted or closed.
type Cursor interface {
	Columns() []Column
	Next() bool
	Values() ([]Value, error)
	Err() error
	RowsAffected() int64 // -1 when the engine did not report one
	Close()
}

// ErrNotFound is returned for an unknown connection, session or saved query.
var ErrNotFound = errors.New("not found")

// ErrReadOnly is returned when a connection marked read-only is asked to write.
var ErrReadOnly = errors.New("this connection is read-only")

// ErrNeedsConfirmation is returned when a statement the classifier flagged is
// run without the confirmation the interface is supposed to have collected.
// The daemon refuses rather than trusting the client to have asked.
var ErrNeedsConfirmation = errors.New("this statement needs an explicit confirmation")

// QueryError is a database error with everything the editor needs to point at
// the thing that went wrong.
type QueryError struct {
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`     // SQLSTATE, or the engine's own code
	Detail   string `json:"detail,omitempty"`   //
	Hint     string `json:"hint,omitempty"`     //
	Position int    `json:"position,omitempty"` // 1-based character offset within the statement, 0 when unknown
}

func (e *QueryError) Error() string { return e.Message }

// asQueryError turns a driver error into one of ours. Drivers register their
// own translation so that this package does not import every driver's error
// types to ask what happened.
func asQueryError(err error) *QueryError {
	if err == nil {
		return nil
	}
	var qe *QueryError
	if errors.As(err, &qe) {
		return qe
	}
	return &QueryError{Message: err.Error()}
}

// registry holds the driver for each engine. Drivers register themselves from
// their own file, so a build that does not want MySQL drops one file and one
// dependency.
var registry = map[string]Driver{}

// RegisterDriver makes a driver available for an engine name.
func RegisterDriver(engine string, d Driver) { registry[engine] = d }

func driverFor(engine string) (Driver, error) {
	if d, ok := registry[engine]; ok {
		return d, nil
	}
	// MariaDB speaks the MySQL protocol and is not worth a second driver.
	if dialectOf(engine) == MySQL {
		if d, ok := registry["mysql"]; ok {
			return d, nil
		}
	}
	return nil, fmt.Errorf("no SQL client driver for %q: this engine is not supported here, use the console or Adminer", engine)
}
