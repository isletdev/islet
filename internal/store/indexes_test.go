package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The queries that used to read whole tables, and the plans they have now.
//
// An index is easy to add and easy to drop again by accident, and the cost of
// dropping one of these is invisible on a laptop with an empty database and
// obvious on a server a year in — check_results grows by 1,440 rows per monitor
// per day, audit_log and commands are capped at 200,000 each. So the plans are
// asserted rather than the indexes: what matters is that SQLite does not say
// SCAN or TEMP B-TREE for work that happens on a timer or on a page load.
func TestTheQueriesThatRunOnEveryPageDoNotScan(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	for _, tc := range []struct{ what, query, wantIndex string }{
		// Asked for from every page in the panel, every sixty seconds, once per
		// monitor, and again on every probe before this was fixed.
		// Naming the index matters here and nowhere else: the older
		// (check_id, id DESC) index satisfies "not a scan" while still reading
		// every retained row for that check and filtering `at` in memory, which
		// is the whole cost this was about.
		{"an uptime percentage", `SELECT count(*), coalesce(sum(ok), 0) FROM check_results WHERE check_id = 'x' AND at >= 'y'`, "check_results_at"},
		// Every six hours, over the largest table in the database.
		{"the uptime purge", `DELETE FROM check_results WHERE check_id = 'x' AND at < 'y'`, "check_results_at"},
		// Nightly, once per deleted event, through the foreign key.
		{"deliveries for one event", `SELECT id FROM deliveries WHERE event_id = 'e'`, ""},
		// Two of the most-visited pages, each sorting up to 200,000 rows.
		{"the audit log page", `SELECT id FROM audit_log WHERE server_id = 's' ORDER BY id DESC LIMIT 100`, ""},
		{"the command drawer", `SELECT id FROM commands WHERE server_id = 's' ORDER BY id DESC LIMIT 100`, ""},
	} {
		rows, err := st.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+tc.query)
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "; ")
		if strings.Contains(joined, "SCAN") || strings.Contains(joined, "TEMP B-TREE") {
			t.Errorf("%s reads more than it needs:\n  %s", tc.what, joined)
		}
		if tc.wantIndex != "" && !strings.Contains(joined, tc.wantIndex) {
			t.Errorf("%s does not use %s:\n  %s", tc.what, tc.wantIndex, joined)
		}
	}
}

// A collection whose names are unique is a collection whose create is safe to
// retry. Three of them were not, and cron was the one that mattered: a retried
// POST left two jobs on the same schedule running the same command forever.
func TestNamesAreUniqueWithinAServer(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if _, err := st.DB.ExecContext(ctx, `INSERT INTO servers (id, name) VALUES ('s1', 'one')`); err != nil {
		// The servers table may already carry this row from Open; either is fine.
		t.Logf("server row: %v", err)
	}
	for _, tc := range []struct{ table, insert string }{
		{"checks", `INSERT INTO checks (id, server_id, name, type, target) VALUES (?, 's1', 'nightly', 'http', 'https://x')`},
		{"channels", `INSERT INTO channels (id, server_id, name, type, config_enc) VALUES (?, 's1', 'nightly', 'webhook', '')`},
		{"jobs", `INSERT INTO jobs (id, server_id, name, type, schedule, command) VALUES (?, 's1', 'nightly', 'command', '0 3 * * *', 'true')`},
	} {
		if _, err := st.DB.ExecContext(ctx, tc.insert, "a"); err != nil {
			t.Fatalf("%s: first insert should work: %v", tc.table, err)
		}
		if _, err := st.DB.ExecContext(ctx, tc.insert, "b"); err == nil {
			t.Errorf("%s accepted a second row with the same name on the same server", tc.table)
		}
	}
}
