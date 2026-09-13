package sqlclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ErrBusy is returned when a tab asks its session to run something while it is
// still running the last thing.
var ErrBusy = errors.New("this session is already running a statement")

// Manager owns every live connection: the pools, the sessions on top of them,
// and the registry that makes cancel reach the server.
//
// Nothing here is created until someone opens a connection, which is the whole
// performance argument: idle, this costs a map and a ticker.
type Manager struct {
	now   func() time.Time
	newID func() string

	mu       sync.Mutex
	pools    map[poolKey]*pool
	sessions map[string]*Session
	running  map[string]*running
}

// running is one statement in flight, so that a cancel can find the connection
// it is on.
type running struct {
	conn   Conn
	user   string
	cancel context.CancelFunc
}

// NewManager builds a manager. now and newID are injected so the janitor and
// the identifiers can be driven deterministically in tests.
func NewManager(now func() time.Time, newID func() string) *Manager {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = randomID
	}
	return &Manager{
		now: now, newID: newID,
		pools:    map[poolKey]*pool{},
		sessions: map[string]*Session{},
		running:  map[string]*running{},
	}
}

// Start runs the janitor until ctx is cancelled: idle connections closed,
// unused pools dropped, abandoned sessions rolled back and released. The
// frontend sends a close on unload and this is what happens when it does not.
func (m *Manager) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				m.CloseAll()
				return
			case <-t.C:
				m.Sweep()
			}
		}
	}()
}

// Sweep is one janitor pass, exported so tests do not have to wait for a tick.
func (m *Manager) Sweep() {
	now := m.now()

	m.mu.Lock()
	var expired []*Session
	for id, s := range m.sessions {
		if now.Sub(s.lastUsed()) >= SessionIdleTimeout {
			delete(m.sessions, id)
			expired = append(expired, s)
		}
	}
	m.mu.Unlock()

	for _, s := range expired {
		// An abandoned session holding an open transaction is the one case
		// where doing nothing is the dangerous option.
		s.close(context.Background(), true)
	}

	m.mu.Lock()
	for key, p := range m.pools {
		if p.reap() {
			delete(m.pools, key)
			go p.close()
		}
	}
	m.mu.Unlock()
}

// CloseAll drops everything. Called on shutdown.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for id, s := range m.sessions {
		sessions = append(sessions, s)
		delete(m.sessions, id)
	}
	pools := make([]*pool, 0, len(m.pools))
	for key, p := range m.pools {
		pools = append(pools, p)
		delete(m.pools, key)
	}
	m.mu.Unlock()

	for _, s := range sessions {
		s.close(context.Background(), true)
	}
	for _, p := range pools {
		p.close()
	}
}

// Forget drops the pools for a connection, which is what has to happen when a
// saved connection is edited or deleted, or when a container is recreated and
// its bridge address changes.
func (m *Manager) Forget(ref string) {
	m.mu.Lock()
	var gone []*pool
	for key, p := range m.pools {
		if key.ref == ref {
			gone = append(gone, p)
			delete(m.pools, key)
		}
	}
	var sessions []*Session
	for id, s := range m.sessions {
		if s.Ref == ref {
			sessions = append(sessions, s)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()

	for _, s := range sessions {
		s.close(context.Background(), true)
	}
	for _, p := range gone {
		p.close()
	}
}

func (m *Manager) poolFor(ref, user string, cfg Config) *pool {
	key := poolKey{ref: ref, user: user}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pools[key]; ok {
		return p
	}
	drv, err := driverFor(cfg.Engine)
	if err != nil {
		return nil // reported by Use and OpenSession, which check the driver first
	}
	p := newPool(cfg, drv, m.now)
	m.pools[key] = p
	return p
}

// Use borrows a connection for one piece of work and returns it. This is the
// path for a connection test, an introspection pass and a one-off query from
// somewhere that has no tab: no session, no dedicated connection, nothing left
// behind.
func (m *Manager) Use(ctx context.Context, ref, user string, cfg Config, fn func(Conn) error) error {
	if _, err := driverFor(cfg.Engine); err != nil {
		return err
	}
	p := m.poolFor(ref, user, cfg)
	conn, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	err = fn(conn)
	if err != nil && !errors.Is(err, ErrReadOnly) && !errors.Is(err, ErrNeedsConfirmation) {
		var qe *QueryError
		if !errors.As(err, &qe) {
			// Not a database error: the connection itself may be unusable.
			p.discard(conn)
			return err
		}
	}
	p.release(conn)
	return err
}

// Session is what one editor tab holds: a dedicated connection, so SET,
// temporary tables and an open transaction all behave the way the user expects
// between one run and the next.
type Session struct {
	ID     string
	Ref    string
	User   string
	Engine string

	m    *Manager
	pool *pool
	conn Conn

	mu       sync.Mutex
	last     time.Time
	busy     bool
	inTx     bool
	closed   bool
	openedAt time.Time
}

// OpenSession takes a dedicated connection for a tab.
func (m *Manager) OpenSession(ctx context.Context, ref, user string, cfg Config) (*Session, error) {
	if _, err := driverFor(cfg.Engine); err != nil {
		return nil, err
	}
	p := m.poolFor(ref, user, cfg)
	conn, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID: m.newID(), Ref: ref, User: user, Engine: cfg.Engine,
		m: m, pool: p, conn: conn,
		last: m.now(), openedAt: m.now(),
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s, nil
}

// LookupSession finds a session, refusing one that belongs to another Islet
// user. A session id is a capability, so it is checked against the actor and
// not merely against existence.
func (m *Manager) LookupSession(id, user string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.User != user {
		return nil, ErrNotFound
	}
	return s, nil
}

// CloseSession closes a tab's session, rolling back anything it left open.
func (m *Manager) CloseSession(ctx context.Context, id, user string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || s.User != user {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	s.close(ctx, true)
	return nil
}

// Sessions lists one user's open sessions.
func (m *Manager) Sessions(user string) []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Session
	for _, s := range m.sessions {
		if s.User == user {
			out = append(out, s)
		}
	}
	return out
}

func (s *Session) lastUsed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// InTransaction reports whether the session is holding an open transaction,
// which the status bar shows and the janitor rolls back.
func (s *Session) InTransaction() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inTx
}

// begin marks the session busy for the duration of one run.
func (s *Session) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrNotFound
	}
	if s.busy {
		return ErrBusy
	}
	s.busy = true
	s.last = s.m.now()
	return nil
}

func (s *Session) end() {
	s.mu.Lock()
	s.busy = false
	s.last = s.m.now()
	s.mu.Unlock()
}

func (s *Session) setInTx(v bool) {
	s.mu.Lock()
	s.inTx = v
	s.mu.Unlock()
}

// close releases the session's connection. rollback asks for an open
// transaction to be undone first, which is always what an abandoned tab wants:
// the alternative is a lock held until someone notices.
func (s *Session) close(ctx context.Context, rollback bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	inTx := s.inTx
	s.inTx = false
	conn := s.conn
	s.mu.Unlock()

	if rollback && inTx {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		if cur, err := conn.Run(c, "ROLLBACK", nil, false); err == nil {
			cur.Close()
		}
		cancel()
	}
	// Discarded rather than returned to the pool. A tab is where SET,
	// temporary tables and search_path happen, and the next borrower would
	// inherit all of it without ever having asked. A session is long-lived
	// enough that one connection per tab is not a cost worth this surprise.
	s.pool.discard(conn)
}

// track registers an in-flight statement so Cancel can find it, and returns
// the function that unregisters it.
func (m *Manager) track(runID, user string, conn Conn, cancel context.CancelFunc) func() {
	m.mu.Lock()
	m.running[runID] = &running{conn: conn, user: user, cancel: cancel}
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.running, runID)
		m.mu.Unlock()
	}
}

// Cancel stops a running statement on the server. Abandoning the response
// would leave the query running and the row lock held, which is exactly the
// situation the user pressed cancel to get out of.
func (m *Manager) Cancel(ctx context.Context, runID, user string) error {
	m.mu.Lock()
	r, ok := m.running[runID]
	m.mu.Unlock()
	if !ok || r.user != user {
		return ErrNotFound
	}
	err := r.conn.Cancel(ctx)
	r.cancel() // and stop waiting for it either way
	return err
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice, and a session id that is not
		// unpredictable is worse than an error.
		panic("sqlclient: no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
