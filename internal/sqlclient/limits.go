package sqlclient

import "time"

// The limits from section 8.1 and 5.1 of the specification. They are constants
// rather than settings because every one of them is a promise the panel makes
// about what this feature can cost a small server, and a setting is a promise
// with an escape hatch.
const (
	// A SELECT on a large table must be boring, not fatal.
	DefaultRowCap = 1_000
	MaxRowCap     = 50_000

	DefaultTimeout = 30 * time.Second
	MaxTimeout     = 10 * time.Minute

	// A panel must not exhaust a small database's connection limit.
	PoolMaxConns    = 4
	PoolIdleTimeout = 5 * time.Minute
	PoolMaxUnused   = 30 * time.Minute

	// A tab's dedicated connection. The frontend sends a close on unload and
	// this is what happens when it does not arrive.
	SessionIdleTimeout = 10 * time.Minute

	SchemaCacheTTL = 60 * time.Second

	// History is pruned to whichever of these is smaller.
	HistoryKeepRows = 10_000
	HistoryKeepDays = 90
)

// ClampRowCap brings a requested cap inside the allowed range. Zero means the
// default; there is no way to ask for unbounded, which is the point.
func ClampRowCap(n int) int {
	switch {
	case n <= 0:
		return DefaultRowCap
	case n > MaxRowCap:
		return MaxRowCap
	default:
		return n
	}
}

// ClampTimeout brings a requested statement timeout inside the allowed range.
func ClampTimeout(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DefaultTimeout
	case d > MaxTimeout:
		return MaxTimeout
	default:
		return d
	}
}
