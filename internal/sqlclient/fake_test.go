package sqlclient

import (
	"context"
	"strings"
	"sync"
	"time"
)

// The fake driver exists so that pooling, sessions, the row cap, the batch
// rules and cancellation are all tested without a database. The real drivers
// are covered by the integration tests, which need containers; everything
// above them is covered here, which needs nothing.

type fakeResult struct {
	cols     []Column
	rows     [][]Value
	affected int64
	err      error
	block    bool // wait for the context or for a cancel, like a slow query
	// deferredErr is a failure the cursor only admits to once it is asked for
	// a row, which is what pgx does for a statement the server cancelled.
	deferredErr error
}

type fakeDriver struct {
	mu       sync.Mutex
	script   map[string]fakeResult
	opened   int
	closed   int
	failOpen error
	conns    []*fakeConn
}

func newFakeDriver() *fakeDriver { return &fakeDriver{script: map[string]fakeResult{}} }

func (d *fakeDriver) on(sql string, r fakeResult) *fakeDriver {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.script[sql] = r
	return d
}

func (d *fakeDriver) Open(ctx context.Context, cfg Config) (Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failOpen != nil {
		return nil, d.failOpen
	}
	d.opened++
	c := &fakeConn{drv: d, cancelled: make(chan struct{})}
	d.conns = append(d.conns, c)
	return c, nil
}

func (d *fakeDriver) counts() (opened, closed int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opened, d.closed
}

type fakeConn struct {
	drv *fakeDriver

	mu        sync.Mutex
	ran       []string
	cancels   int
	closed    bool
	cancelled chan struct{}
}

func (c *fakeConn) Ping(context.Context) error { return nil }

func (c *fakeConn) Version(context.Context) (string, error) { return "Fake 1.0", nil }

func (c *fakeConn) Run(ctx context.Context, sql string, args []any, wantRows bool) (Cursor, error) {
	c.mu.Lock()
	c.ran = append(c.ran, sql)
	c.mu.Unlock()

	c.drv.mu.Lock()
	r, ok := c.drv.script[sql]
	c.drv.mu.Unlock()
	if !ok {
		r = fakeResult{affected: 0}
	}
	if r.block {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.cancelled:
			return nil, context.Canceled
		case <-time.After(5 * time.Second):
			return nil, context.DeadlineExceeded
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	return &fakeCursor{cols: r.cols, rows: r.rows, affected: r.affected, deferred: r.deferredErr}, nil
}

func (c *fakeConn) Cancel(context.Context) error {
	c.mu.Lock()
	c.cancels++
	select {
	case <-c.cancelled:
	default:
		close(c.cancelled)
	}
	c.mu.Unlock()
	return nil
}

func (c *fakeConn) Close(context.Context) error {
	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()
	if !already {
		c.drv.mu.Lock()
		c.drv.closed++
		c.drv.mu.Unlock()
	}
	return nil
}

func (c *fakeConn) statements() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.ran...)
}

func (c *fakeConn) cancelCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancels
}

type fakeCursor struct {
	cols     []Column
	rows     [][]Value
	affected int64
	i        int
	closed   bool
	deferred error
	asked    bool
}

func (f *fakeCursor) Columns() []Column { return f.cols }

func (f *fakeCursor) Next() bool {
	f.asked = true
	if f.i >= len(f.rows) {
		return false
	}
	f.i++
	return true
}

func (f *fakeCursor) Values() ([]Value, error) { return f.rows[f.i-1], nil }
func (f *fakeCursor) Err() error {
	// Only after Next, exactly as a real driver reports a deferred failure.
	if f.asked {
		return f.deferred
	}
	return nil
}
func (f *fakeCursor) RowsAffected() int64 { return f.affected }
func (f *fakeCursor) Close()              { f.closed = true }

// clock is a hand-wound time source for the janitor tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// testTarget wires a fake driver into the registry under a unique engine name
// and returns everything a test needs to run against it.
func testTarget(t interface{ Cleanup(func()) }, engine string, d *fakeDriver) (*Manager, Target, *clock) {
	RegisterDriver(engine, d)
	t.Cleanup(func() { delete(registry, engine) })
	clk := newClock()
	ids := 0
	m := NewManager(clk.now, func() string { ids++; return "sess" + string(rune('0'+ids)) })
	tgt := Target{
		Ref: "testdb", User: "alice", Engine: engine,
		Config: Config{Engine: engine, Host: "127.0.0.1", Port: 5432, User: "postgres", Database: "app"},
	}
	return m, tgt, clk
}

// rows collects the emitted statement results of a run.
func collect(results *[]StatementResult) func(StatementResult) error {
	return func(r StatementResult) error {
		*results = append(*results, r)
		return nil
	}
}

func rowsOf(n int) [][]Value {
	out := make([][]Value, n)
	for i := range out {
		out[i] = []Value{int64(i)}
	}
	return out
}

func joined(ss []string) string { return strings.Join(ss, " | ") }
