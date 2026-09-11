// Package uptime runs HTTP, TCP and keyword checks from this server and
// raises events when a target goes down or comes back.
package uptime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// Check is one monitored target.
type Check struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Target       string  `json:"target"`
	Keyword      string  `json:"keyword"`
	IntervalSec  int     `json:"intervalSec"`
	TimeoutSec   int     `json:"timeoutSec"`
	ExpectStatus int     `json:"expectStatus"`
	Enabled      bool    `json:"enabled"`
	Status       string  `json:"status"`
	Failures     int     `json:"failures"`
	LastCheckAt  string  `json:"lastCheckAt"`
	LastLatency  int     `json:"lastLatencyMs"`
	LastError    string  `json:"lastError"`
	DownSince    string  `json:"downSince"`
	CreatedAt    string  `json:"createdAt"`
	Uptime24h    float64 `json:"uptime24h"`
	Uptime30d    float64 `json:"uptime30d"`
}

// Result is one probe.
type Result struct {
	At        string `json:"at"`
	OK        bool   `json:"ok"`
	LatencyMs int    `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// ErrNotFound is returned for unknown checks.
var ErrNotFound = errors.New("check not found")

const sqlTime = "2006-01-02T15:04:05.000Z"

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._:/-]{0,79}$`)

// Service schedules and runs checks.
type Service struct {
	st  *store.Store
	bus *notify.Bus

	mu     sync.Mutex
	timers map[string]context.CancelFunc
	client *http.Client
}

// New builds the service.
func New(st *store.Store, bus *notify.Bus) *Service {
	return &Service{st: st, bus: bus, timers: map[string]context.CancelFunc{}, client: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}}
}

// Start loads checks and begins probing.
func (s *Service) Start(ctx context.Context) error {
	list, err := s.List(ctx)
	if err != nil {
		return err
	}
	for i := range list {
		s.schedule(ctx, &list[i])
	}
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			s.purge(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}

func (s *Service) purge(ctx context.Context) {
	cut := time.Now().Add(-31 * 24 * time.Hour).UTC().Format(sqlTime)
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM check_results WHERE at < ?`, cut)
}

func (s *Service) schedule(ctx context.Context, c *Check) {
	s.mu.Lock()
	if cancel, ok := s.timers[c.ID]; ok {
		cancel()
		delete(s.timers, c.ID)
	}
	if !c.Enabled {
		s.mu.Unlock()
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	s.timers[c.ID] = cancel
	s.mu.Unlock()
	id := c.ID
	interval := time.Duration(c.IntervalSec) * time.Second
	go func() {
		// Spread the first probe so a restart does not fire every check at once.
		select {
		case <-cctx.Done():
			return
		case <-time.After(time.Duration(randInt(int(interval.Seconds()))) * time.Second):
		}
		for {
			s.probe(cctx, id)
			select {
			case <-cctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

// Validate normalises a check before saving.
func (c *Check) Validate() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Target = strings.TrimSpace(c.Target)
	if !nameRe.MatchString(c.Name) {
		return errors.New("name must be 1-80 letters, digits, spaces, dots, dashes or slashes")
	}
	switch c.Type {
	case "http", "keyword":
		u, err := url.Parse(c.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("target must be an http:// or https:// URL")
		}
		if c.Type == "keyword" && strings.TrimSpace(c.Keyword) == "" {
			return errors.New("keyword is required")
		}
	case "tcp":
		if _, _, err := net.SplitHostPort(c.Target); err != nil {
			return errors.New("target must be host:port")
		}
	default:
		return errors.New("type must be http, tcp or keyword")
	}
	if c.IntervalSec == 0 {
		c.IntervalSec = 60
	}
	if c.IntervalSec < 20 || c.IntervalSec > 86400 {
		return errors.New("interval must be between 20 seconds and one day")
	}
	if c.TimeoutSec == 0 {
		c.TimeoutSec = 10
	}
	if c.TimeoutSec < 1 || c.TimeoutSec > 60 {
		return errors.New("timeout must be between 1 and 60 seconds")
	}
	if c.ExpectStatus != 0 && (c.ExpectStatus < 100 || c.ExpectStatus > 599) {
		return errors.New("expected status must be an HTTP status code")
	}
	return nil
}

const cols = `id, name, type, target, keyword, interval_sec, timeout_sec, expect_status, enabled, status, failures, last_check_at, last_latency, last_error, down_since, created_at`

func scan(sc interface{ Scan(...any) error }) (Check, error) {
	var c Check
	err := sc.Scan(&c.ID, &c.Name, &c.Type, &c.Target, &c.Keyword, &c.IntervalSec, &c.TimeoutSec, &c.ExpectStatus, &c.Enabled, &c.Status, &c.Failures, &c.LastCheckAt, &c.LastLatency, &c.LastError, &c.DownSince, &c.CreatedAt)
	return c, err
}

// List returns every check with uptime percentages.
func (s *Service) List(ctx context.Context) ([]Check, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM checks WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		c.Uptime24h, c.Uptime30d = s.uptime(ctx, c.ID)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one check.
func (s *Service) Get(ctx context.Context, id string) (*Check, error) {
	c, err := scan(s.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM checks WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Uptime24h, c.Uptime30d = s.uptime(ctx, c.ID)
	return &c, nil
}

func (s *Service) uptime(ctx context.Context, id string) (float64, float64) {
	pct := func(d time.Duration) float64 {
		cut := time.Now().Add(-d).UTC().Format(sqlTime)
		var total, ok int
		_ = s.st.DB.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(ok), 0) FROM check_results WHERE check_id = ? AND at >= ?`, id, cut).Scan(&total, &ok)
		if total == 0 {
			return -1
		}
		return float64(ok) / float64(total) * 100
	}
	return pct(24 * time.Hour), pct(30 * 24 * time.Hour)
}

// Save inserts or updates a check and reschedules it.
func (s *Service) Save(ctx context.Context, c *Check) (*Check, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.ID == "" {
		c.ID = randID()
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO checks (id, server_id, name, type, target, keyword, interval_sec, timeout_sec, expect_status, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, s.st.ServerID, c.Name, c.Type, c.Target, c.Keyword, c.IntervalSec, c.TimeoutSec, c.ExpectStatus, c.Enabled)
		if err != nil {
			return nil, err
		}
	} else {
		if _, err := s.Get(ctx, c.ID); err != nil {
			return nil, err
		}
		_, err := s.st.DB.ExecContext(ctx, `UPDATE checks SET name = ?, type = ?, target = ?, keyword = ?, interval_sec = ?, timeout_sec = ?, expect_status = ?, enabled = ?, status = CASE WHEN ? THEN status ELSE 'paused' END WHERE id = ?`,
			c.Name, c.Type, c.Target, c.Keyword, c.IntervalSec, c.TimeoutSec, c.ExpectStatus, c.Enabled, c.Enabled, c.ID)
		if err != nil {
			return nil, err
		}
	}
	saved, err := s.Get(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	s.schedule(context.Background(), saved)
	return saved, nil
}

// Delete removes a check and its history.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	if cancel, ok := s.timers[id]; ok {
		cancel()
		delete(s.timers, id)
	}
	s.mu.Unlock()
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM check_results WHERE check_id = ?`, id)
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM checks WHERE id = ? AND server_id = ?`, id, s.st.ServerID)
	return err
}

// Results returns recent probes, newest first.
func (s *Service) Results(ctx context.Context, id string, limit int) ([]Result, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT at, ok, latency_ms, error FROM check_results WHERE check_id = ? ORDER BY id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.At, &r.OK, &r.LatencyMs, &r.Error); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// Probe runs one check immediately and returns the result.
func (s *Service) Probe(ctx context.Context, id string) (*Result, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	r := s.run(ctx, c)
	return &r, nil
}

func (s *Service) probe(ctx context.Context, id string) {
	c, err := s.Get(ctx, id)
	if err != nil || !c.Enabled {
		return
	}
	r := s.run(ctx, c)
	_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO check_results (check_id, ok, latency_ms, error) VALUES (?, ?, ?, ?)`, c.ID, r.OK, r.LatencyMs, r.Error)
	now := time.Now().UTC().Format(sqlTime)
	if r.OK {
		wasDown := c.Status == "down"
		_, _ = s.st.DB.ExecContext(ctx, `UPDATE checks SET status = 'up', failures = 0, last_check_at = ?, last_latency = ?, last_error = '', down_since = '' WHERE id = ?`, now, r.LatencyMs, c.ID)
		if wasDown && s.bus != nil {
			since, _ := time.Parse(sqlTime, c.DownSince)
			s.bus.Emit(ctx, notify.Event{Category: "uptime", Subject: c.Name, Severity: notify.Info, Title: "Down: " + c.Name, Message: fmt.Sprintf("Recovered after %s. %s answers again in %d ms.", time.Since(since).Round(time.Second), c.Target, r.LatencyMs), Link: "/uptime"})
		}
		return
	}
	failures := c.Failures + 1
	// Two consecutive failures mark a target down, so one blip does not page anyone.
	if failures >= 2 && c.Status != "down" {
		_, _ = s.st.DB.ExecContext(ctx, `UPDATE checks SET status = 'down', failures = ?, last_check_at = ?, last_latency = ?, last_error = ?, down_since = ? WHERE id = ?`, failures, now, r.LatencyMs, r.Error, now, c.ID)
		if s.bus != nil {
			s.bus.Emit(ctx, notify.Event{Category: "uptime", Subject: c.Name, Severity: notify.Critical, Title: "Down: " + c.Name, Message: c.Target + ": " + r.Error, Link: "/uptime"})
		}
		return
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE checks SET failures = ?, last_check_at = ?, last_latency = ?, last_error = ? WHERE id = ?`, failures, now, r.LatencyMs, r.Error, c.ID)
}

func (s *Service) run(ctx context.Context, c *Check) Result {
	tctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSec)*time.Second)
	defer cancel()
	start := time.Now()
	var err error
	switch c.Type {
	case "tcp":
		var conn net.Conn
		conn, err = (&net.Dialer{}).DialContext(tctx, "tcp", c.Target)
		if conn != nil {
			conn.Close()
		}
	default:
		err = s.httpProbe(tctx, c)
	}
	r := Result{At: time.Now().UTC().Format(sqlTime), OK: err == nil, LatencyMs: int(time.Since(start).Milliseconds())}
	if err != nil {
		msg := err.Error()
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			msg = fmt.Sprintf("no answer within %ds", c.TimeoutSec)
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		r.Error = msg
	}
	return r
}

func (s *Service) httpProbe(ctx context.Context, c *Check) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "islet-uptime/1")
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if c.ExpectStatus != 0 {
		if res.StatusCode != c.ExpectStatus {
			return fmt.Errorf("HTTP %d, expected %d", res.StatusCode, c.ExpectStatus)
		}
	} else if res.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if c.Type == "keyword" {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if !strings.Contains(string(body), c.Keyword) {
			return fmt.Errorf("keyword %q not found in the response", c.Keyword)
		}
	}
	return nil
}

func randID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randInt(n int) int {
	if n <= 1 {
		return 0
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return int(uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3])) % n
}
