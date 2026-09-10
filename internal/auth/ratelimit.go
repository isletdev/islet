package auth

import (
	"sync"
	"time"
)

// Limiter is a small in-memory sliding-window limiter for login attempts,
// keyed by whatever the caller passes (client IP, username).
type Limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

// NewLimiter allows `limit` failures per key within `window`.
func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{hits: make(map[string][]time.Time), limit: limit, window: window}
}

// Allow reports whether key is under the limit right now.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, now)) < l.limit
}

// Fail records a failed attempt for key.
func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.prune(key, now), now)
}

// Reset clears the key after a successful attempt.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// RetryAfter says how long until the oldest counted failure expires.
func (l *Limiter) RetryAfter(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.prune(key, now)
	if len(h) < l.limit {
		return 0
	}
	return h[0].Add(l.window).Sub(now)
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	h := l.hits[key]
	cut := now.Add(-l.window)
	i := 0
	for i < len(h) && h[i].Before(cut) {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}
