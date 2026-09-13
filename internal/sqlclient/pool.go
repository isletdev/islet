package sqlclient

import (
	"context"
	"sync"
	"time"
)

// poolKey is one pool per connection and Islet user. Not per tab: a tab is a
// session on top of a pool. Per user, because the audit trail and the
// database's own process list should both be able to say who was asking.
type poolKey struct {
	ref  string // instance name or saved connection id
	user string // Islet username
}

type pooled struct {
	conn      Conn
	idleSince time.Time
}

// pool hands out at most PoolMaxConns connections to one database. The token
// channel is the limit: a caller holds a token for as long as it holds a
// connection, so waiting for a free connection is waiting for a token, and it
// respects the caller's context instead of blocking forever.
type pool struct {
	cfg    Config
	drv    Driver
	now    func() time.Time
	tokens chan struct{}

	mu      sync.Mutex
	idle    []*pooled
	inUse   int
	lastUse time.Time
	closed  bool
}

func newPool(cfg Config, drv Driver, now func() time.Time) *pool {
	p := &pool{cfg: cfg, drv: drv, now: now, tokens: make(chan struct{}, PoolMaxConns), lastUse: now()}
	for i := 0; i < PoolMaxConns; i++ {
		p.tokens <- struct{}{}
	}
	return p
}

// acquire returns a connection, opening one if the pool is below its limit and
// waiting for one otherwise.
func (p *pool) acquire(ctx context.Context) (Conn, error) {
	select {
	case <-p.tokens:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.tokens <- struct{}{}
		return nil, ErrNotFound
	}
	p.inUse++
	p.lastUse = p.now()
	var reuse Conn
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if p.now().Sub(c.idleSince) < PoolIdleTimeout {
			reuse = c.conn
			break
		}
		go c.conn.Close(context.Background()) // too old to trust
	}
	p.mu.Unlock()

	if reuse != nil {
		// An idle connection can have been closed by the server. One ping is
		// cheaper than an error the user cannot explain.
		if err := reuse.Ping(ctx); err == nil {
			return reuse, nil
		}
		_ = reuse.Close(context.Background())
	}

	conn, err := p.drv.Open(ctx, p.cfg)
	if err != nil {
		p.mu.Lock()
		p.inUse--
		p.mu.Unlock()
		p.tokens <- struct{}{}
		return nil, err
	}
	return conn, nil
}

// release returns a healthy connection to the pool.
func (p *pool) release(c Conn) {
	p.mu.Lock()
	p.inUse--
	p.lastUse = p.now()
	if p.closed {
		p.mu.Unlock()
		_ = c.Close(context.Background())
		p.tokens <- struct{}{}
		return
	}
	p.idle = append(p.idle, &pooled{conn: c, idleSince: p.now()})
	p.mu.Unlock()
	p.tokens <- struct{}{}
}

// discard drops a connection that is no longer trustworthy: a failed query
// that may have left it mid-protocol, or a cancelled one.
func (p *pool) discard(c Conn) {
	p.mu.Lock()
	p.inUse--
	p.lastUse = p.now()
	p.mu.Unlock()
	_ = c.Close(context.Background())
	p.tokens <- struct{}{}
}

// reap closes connections that have sat idle too long and reports whether the
// whole pool has gone unused long enough to be thrown away.
func (p *pool) reap() (dead bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	kept := p.idle[:0]
	for _, c := range p.idle {
		if now.Sub(c.idleSince) >= PoolIdleTimeout {
			go c.conn.Close(context.Background())
			continue
		}
		kept = append(kept, c)
	}
	p.idle = kept
	return p.inUse == 0 && len(p.idle) == 0 && now.Sub(p.lastUse) >= PoolMaxUnused
}

// close shuts the pool down. Connections handed out are closed when they come
// back, which is why release checks closed.
func (p *pool) close() {
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, c := range idle {
		_ = c.conn.Close(context.Background())
	}
}

// stats is what the health endpoint and the tests want to know.
func (p *pool) stats() (inUse, idle int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inUse, len(p.idle)
}
