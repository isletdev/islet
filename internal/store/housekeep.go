package store

import (
	"context"
	"fmt"
)

// How long the two self-growing tables are kept.
//
// They answer different questions, so they get different answers. The command
// drawer is "what has this panel been running", which is a question about now:
// a fortnight covers the week somebody spends wondering why a deploy behaved
// oddly. The audit log is "who did what to this server", which is a question
// about the past, and a year is the least that is useful when it is asked.
//
// Measured on a live server before these existed: 3,600 commands and 700 audit
// rows a day, neither with a ceiling — 1.3 million and 250,000 rows a year, in
// a database whose whole point is to be small enough to back up in a moment.
const (
	CommandRetentionDays = 14
	AuditRetentionDays   = 365

	// A cap as well as an age, because a runaway loop can write a fortnight's
	// worth in an hour and the age limit would not notice until tomorrow.
	maxCommandRows = 200_000
	maxAuditRows   = 200_000
)

// Housekeep bounds the tables that grow without anybody asking them to.
//
// It belongs here rather than in the packages that write those rows: the schema
// is this package's, the two tables are shared by every feature, and a feature
// package deleting another's history would be exactly the sort of reach across
// the middle that the rest of this codebase avoids.
func (s *Store) Housekeep(ctx context.Context) error {
	for _, t := range []struct {
		table string
		days  int
		max   int
	}{
		{"commands", CommandRetentionDays, maxCommandRows},
		{"audit_log", AuditRetentionDays, maxAuditRows},
	} {
		if _, err := s.DB.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE server_id = ? AND created_at < strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now', '-%d days')`, t.table, t.days),
			s.ServerID); err != nil {
			return fmt.Errorf("prune %s by age: %w", t.table, err)
		}
		// Then by count, keeping the newest. id is monotonic here, so "newest"
		// needs no date arithmetic.
		if _, err := s.DB.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE server_id = ? AND id NOT IN (
				SELECT id FROM %s WHERE server_id = ? ORDER BY id DESC LIMIT ?)`, t.table, t.table),
			s.ServerID, s.ServerID, t.max); err != nil {
			return fmt.Errorf("prune %s by count: %w", t.table, err)
		}
	}
	return nil
}
