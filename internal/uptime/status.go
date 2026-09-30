package uptime

import (
	"context"
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

	for _, r := range found {
		days, pct := s.history(ctx, r.id, 30)
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

// history is one bucket per day, oldest first, with a gap where there is no
// data rather than a hole in the middle of a bar chart.
func (s *Service) history(ctx context.Context, id string, days int) ([]Day, float64) {
	cut := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT substr(at, 1, 10) AS day, count(*), coalesce(sum(ok), 0)
		   FROM check_results WHERE check_id = ? AND at >= ? GROUP BY day`, id, cut)
	if err != nil {
		return nil, -1
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]Day{}
	var total, ok int
	for rows.Next() {
		var d string
		var n, up int
		if err := rows.Scan(&d, &n, &up); err != nil {
			return nil, -1
		}
		seen[d] = Day{Date: d, Up: up, Down: n - up}
		total += n
		ok += up
	}
	out := make([]Day, 0, days)
	for i := days - 1; i >= 0; i-- {
		d := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		if got, hit := seen[d]; hit {
			out = append(out, got)
			continue
		}
		out = append(out, Day{Date: d})
	}
	if total == 0 {
		return out, -1
	}
	return out, float64(ok) / float64(total) * 100
}
