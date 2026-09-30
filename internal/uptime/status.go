package uptime

import (
	"context"
	"strings"
	"time"
)

// What a stranger sees.
//
// The status page is the only thing this panel serves to people who are not the
// operator: the customer wondering whether it is them or the site. Everything
// here is therefore deliberately thin — a name, a state, a history of days, and
// nothing else. In particular a check's *target* never leaves this file, because
// "which URLs this company monitors" is not a thing a status page is for.
//
// Thirty days rather than the ninety such pages usually show, because results
// are kept for thirty-one: a ninety-day bar would be sixty days of blank
// pretending to be sixty days of green.

// Day is one day of a check's history.
type Day struct {
	Date string `json:"date"`
	// Up and Down are how many probes answered and how many did not. Both zero
	// is a day with no data — before the check existed, or while the daemon was
	// off — and is drawn as a gap rather than as a failure.
	Up   int `json:"up"`
	Down int `json:"down"`
}

// Summary is one public check.
type Summary struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Uptime30d   float64 `json:"uptime30d"`
	LastLatency int     `json:"lastLatencyMs"`
	DownSince   string  `json:"downSince,omitempty"`
	Days        []Day   `json:"days"`
}

// PublicStatus is the page's whole content.
type PublicStatus struct {
	Checks    []Summary `json:"checks"`
	Down      int       `json:"down"`
	CheckedAt string    `json:"checkedAt"`
}

// Public reads the checks somebody has chosen to show, with a month of history.
func (s *Service) Public(ctx context.Context) (PublicStatus, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, name, status, last_latency, down_since, last_check_at
		   FROM checks WHERE server_id = ? AND public = 1 AND enabled = 1 ORDER BY name`, s.st.ServerID)
	if err != nil {
		return PublicStatus{}, err
	}
	defer func() { _ = rows.Close() }()

	out := PublicStatus{Checks: []Summary{}}
	type row struct {
		id, lastCheck string
		sum           Summary
	}
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sum.Name, &r.sum.Status, &r.sum.LastLatency, &r.sum.DownSince, &r.lastCheck); err != nil {
			return PublicStatus{}, err
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return PublicStatus{}, err
	}

	// One query for every check rather than one per check. The aggregate is
	// over the largest table here — a check at sixty seconds keeps 44,640 rows
	// — and doing it in a loop made a page a stranger can request cost seconds
	// of the only core this server has. Measured at 4.7 s for 52 checks before
	// this, which is also the panel being unusable for that long.
	ids := make([]string, 0, len(found))
	for _, r := range found {
		ids = append(ids, r.id)
	}
	buckets, err := s.historyFor(ctx, ids, 30)
	if err != nil {
		return PublicStatus{}, err
	}
	for _, r := range found {
		days, pct := fill(buckets[r.id], 30)
		r.sum.Days, r.sum.Uptime30d = days, pct
		if r.sum.Status == "down" {
			out.Down++
		}
		if r.lastCheck > out.CheckedAt {
			out.CheckedAt = r.lastCheck
		}
		out.Checks = append(out.Checks, r.sum)
	}
	return out, nil
}

// historyFor counts each day's probes for every check at once.
func (s *Service) historyFor(ctx context.Context, ids []string, days int) (map[string]map[string]Day, error) {
	out := map[string]map[string]Day{}
	if len(ids) == 0 {
		return out, nil
	}
	cut := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	args := make([]any, 0, len(ids)+1)
	holes := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		holes = append(holes, "?")
	}
	args = append(args, cut)
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT check_id, substr(at, 1, 10) AS day, count(*), coalesce(sum(ok), 0)
		   FROM check_results WHERE check_id IN (`+strings.Join(holes, ",")+`) AND at >= ?
		  GROUP BY check_id, day`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, day string
		var n, up int
		if err := rows.Scan(&id, &day, &n, &up); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]Day{}
		}
		out[id][day] = Day{Date: day, Up: up, Down: n - up}
	}
	return out, rows.Err()
}

// fill turns what was counted into one bucket per day, oldest first, with a gap
// where there is no data rather than a hole in the middle of a bar chart.
func fill(seen map[string]Day, days int) ([]Day, float64) {
	out := make([]Day, 0, days)
	var total, ok int
	for i := days - 1; i >= 0; i-- {
		d := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		if got, hit := seen[d]; hit {
			out = append(out, got)
			total += got.Up + got.Down
			ok += got.Up
			continue
		}
		out = append(out, Day{Date: d})
	}
	if total == 0 {
		return out, -1
	}
	return out, float64(ok) / float64(total) * 100
}
