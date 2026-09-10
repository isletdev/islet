// Package notify is the event bus. Features emit typed events; channels the
// user configured receive them according to category, severity and quiet
// hours; a persisted outbox retries failed deliveries.
package notify

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

// Severity levels, in order.
const (
	Info     = "info"
	Warning  = "warning"
	Critical = "critical"
)

var sevRank = map[string]int{Info: 0, Warning: 1, Critical: 2}

// sqlTime matches SQLite's strftime('%Y-%m-%dT%H:%M:%fZ') so string comparisons work.
const sqlTime = "2006-01-02T15:04:05.000Z"

// ParseTime reads timestamps written by SQLite defaults.
func ParseTime(s string) time.Time {
	t, _ := time.Parse(sqlTime, s)
	return t
}

// Event is something that happened on the server.
type Event struct {
	ID        int64  `json:"id"`
	Category  string `json:"category"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Link      string `json:"link"`
	CreatedAt string `json:"createdAt"`
}

// Channel is a configured destination. Config holds the secrets and is
// only returned to admins.
type Channel struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Config      map[string]string `json:"config,omitempty"`
	Categories  string            `json:"categories"`
	MinSeverity string            `json:"minSeverity"`
	QuietFrom   string            `json:"quietFrom"`
	QuietTo     string            `json:"quietTo"`
	Enabled     bool              `json:"enabled"`
	CreatedAt   string            `json:"createdAt"`
}

// Bus stores events and drives deliveries.
type Bus struct {
	st       *store.Store
	keys     *auth.Keys
	log      *slog.Logger
	hostname string
	panelURL string

	mu       sync.Mutex
	cooldown map[string]time.Time
	wake     chan struct{}
}

// New builds the bus.
func New(st *store.Store, keys *auth.Keys, log *slog.Logger) *Bus {
	return &Bus{st: st, keys: keys, log: log, hostname: st.Hostname, cooldown: map[string]time.Time{}, wake: make(chan struct{}, 1)}
}

// SetPanelURL sets the base for deep links in messages.
func (b *Bus) SetPanelURL(u string) { b.panelURL = strings.TrimRight(u, "/") }

// Emit records an event and queues deliveries. Warnings and criticals with
// the same category and title are suppressed for 10 minutes; recoveries
// (info) always go through so the loop closes.
func (b *Bus) Emit(ctx context.Context, e Event) {
	if sevRank[e.Severity] == 0 && e.Severity != Info {
		e.Severity = Info
	}
	key := e.Category + "|" + e.Title
	b.mu.Lock()
	if e.Severity != Info {
		if t, ok := b.cooldown[key]; ok && time.Since(t) < 10*time.Minute {
			b.mu.Unlock()
			return
		}
		b.cooldown[key] = time.Now()
	} else {
		delete(b.cooldown, key)
	}
	b.mu.Unlock()

	res, err := b.st.DB.ExecContext(ctx, `INSERT INTO events (server_id, category, severity, title, message, link) VALUES (?, ?, ?, ?, ?, ?)`,
		b.st.ServerID, e.Category, e.Severity, e.Title, e.Message, e.Link)
	if err != nil {
		b.log.Warn("event insert failed", "err", err)
		return
	}
	id, _ := res.LastInsertId()
	chans, err := b.Channels(ctx, true)
	if err != nil {
		return
	}
	now := time.Now()
	for _, c := range chans {
		if !c.Enabled || !c.accepts(e, now) {
			continue
		}
		_, _ = b.st.DB.ExecContext(ctx, `INSERT INTO deliveries (event_id, channel_id) VALUES (?, ?)`, id, c.ID)
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (c Channel) accepts(e Event, now time.Time) bool {
	if sevRank[e.Severity] < sevRank[c.MinSeverity] {
		return false
	}
	if c.Categories != "*" && c.Categories != "" {
		ok := false
		for _, cat := range strings.Split(c.Categories, ",") {
			if strings.TrimSpace(cat) == e.Category {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if c.QuietFrom != "" && c.QuietTo != "" && e.Severity != Critical && inQuiet(now, c.QuietFrom, c.QuietTo) {
		return false
	}
	return true
}

func inQuiet(now time.Time, from, to string) bool {
	parse := func(s string) (int, bool) {
		var h, m int
		if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
			return 0, false
		}
		return h*60 + m, true
	}
	f, ok1 := parse(from)
	t, ok2 := parse(to)
	if !ok1 || !ok2 {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if f <= t {
		return cur >= f && cur < t
	}
	return cur >= f || cur < t // overnight window
}

// Run delivers pending rows until ctx ends.
func (b *Bus) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		b.deliverPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-b.wake:
		}
	}
}

func (b *Bus) deliverPending(ctx context.Context) {
	rows, err := b.st.DB.QueryContext(ctx, `SELECT d.id, d.channel_id, d.attempts, e.id, e.category, e.severity, e.title, e.message, e.link, e.created_at
		FROM deliveries d JOIN events e ON e.id = d.event_id
		WHERE d.status = 'pending' AND d.next_try_at <= strftime('%Y-%m-%dT%H:%M:%fZ','now') ORDER BY d.id LIMIT 50`)
	if err != nil {
		return
	}
	type item struct {
		id, attempts int
		chID         string
		ev           Event
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.chID, &it.attempts, &it.ev.ID, &it.ev.Category, &it.ev.Severity, &it.ev.Title, &it.ev.Message, &it.ev.Link, &it.ev.CreatedAt); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		ch, err := b.Channel(ctx, it.chID, true)
		if err != nil {
			_, _ = b.st.DB.ExecContext(ctx, `UPDATE deliveries SET status = 'failed', error = 'channel missing' WHERE id = ?`, it.id)
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = Send(sendCtx, *ch, b.render(it.ev))
		cancel()
		if err == nil {
			_, _ = b.st.DB.ExecContext(ctx, `UPDATE deliveries SET status = 'sent', attempts = attempts + 1, sent_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), error = '' WHERE id = ?`, it.id)
			continue
		}
		attempts := it.attempts + 1
		if attempts >= 5 {
			_, _ = b.st.DB.ExecContext(ctx, `UPDATE deliveries SET status = 'failed', attempts = ?, error = ? WHERE id = ?`, attempts, err.Error(), it.id)
			b.log.Warn("notification failed permanently", "channel", ch.Name, "err", err)
			continue
		}
		next := time.Now().Add(time.Duration(30*attempts*attempts) * time.Second).UTC().Format(sqlTime)
		_, _ = b.st.DB.ExecContext(ctx, `UPDATE deliveries SET attempts = ?, next_try_at = ?, error = ? WHERE id = ?`, attempts, next, err.Error(), it.id)
	}
}

// Message is what a channel sends: one shape for every channel.
type Message struct {
	Severity string
	Server   string
	Title    string
	Body     string
	Link     string
	Category string
	Time     time.Time
}

func (b *Bus) render(e Event) Message {
	m := Message{Severity: e.Severity, Server: b.hostname, Title: e.Title, Body: e.Message, Category: e.Category}
	m.Time = ParseTime(e.CreatedAt)
	if e.Link != "" && b.panelURL != "" {
		m.Link = b.panelURL + e.Link
	}
	return m
}

// ---- channels ----

// Channels lists channels; withConfig decrypts the secrets.
func (b *Bus) Channels(ctx context.Context, withConfig bool) ([]Channel, error) {
	rows, err := b.st.DB.QueryContext(ctx, `SELECT id, type, name, config_enc, categories, min_severity, quiet_from, quiet_to, enabled, created_at FROM channels WHERE server_id = ? ORDER BY name`, b.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		c, err := b.scanChannel(rows, withConfig)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Channel loads one channel.
func (b *Bus) Channel(ctx context.Context, id string, withConfig bool) (*Channel, error) {
	row := b.st.DB.QueryRowContext(ctx, `SELECT id, type, name, config_enc, categories, min_severity, quiet_from, quiet_to, enabled, created_at FROM channels WHERE id = ? AND server_id = ?`, id, b.st.ServerID)
	return b.scanChannel(row, withConfig)
}

func (b *Bus) scanChannel(sc interface{ Scan(...any) error }, withConfig bool) (*Channel, error) {
	var c Channel
	var enc []byte
	var en int
	if err := sc.Scan(&c.ID, &c.Type, &c.Name, &enc, &c.Categories, &c.MinSeverity, &c.QuietFrom, &c.QuietTo, &en, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.Enabled = en == 1
	if withConfig {
		plain, err := b.keys.Decrypt(enc)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal(plain, &c.Config)
	}
	return &c, nil
}

// SaveChannel validates, encrypts and stores a channel.
func (b *Bus) SaveChannel(ctx context.Context, c *Channel) (*Channel, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return nil, errors.New("name is required")
	}
	if _, ok := sevRank[c.MinSeverity]; !ok {
		c.MinSeverity = Warning
	}
	if err := ValidateConfig(c.Type, c.Config); err != nil {
		return nil, err
	}
	plain, _ := json.Marshal(c.Config)
	enc, err := b.keys.Encrypt(plain)
	if err != nil {
		return nil, err
	}
	en := 0
	if c.Enabled {
		en = 1
	}
	if c.ID == "" {
		c.ID = newID()
		_, err = b.st.DB.ExecContext(ctx, `INSERT INTO channels (id, server_id, type, name, config_enc, categories, min_severity, quiet_from, quiet_to, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, b.st.ServerID, c.Type, c.Name, enc, c.Categories, c.MinSeverity, c.QuietFrom, c.QuietTo, en)
	} else {
		_, err = b.st.DB.ExecContext(ctx, `UPDATE channels SET type=?, name=?, config_enc=?, categories=?, min_severity=?, quiet_from=?, quiet_to=?, enabled=? WHERE id=? AND server_id=?`,
			c.Type, c.Name, enc, c.Categories, c.MinSeverity, c.QuietFrom, c.QuietTo, en, c.ID, b.st.ServerID)
	}
	if err != nil {
		return nil, err
	}
	return b.Channel(ctx, c.ID, true)
}

// DeleteChannel removes a channel and its deliveries.
func (b *Bus) DeleteChannel(ctx context.Context, id string) error {
	_, err := b.st.DB.ExecContext(ctx, `DELETE FROM channels WHERE id = ? AND server_id = ?`, id, b.st.ServerID)
	return err
}

// Test sends a sample message to one channel right away.
func (b *Bus) Test(ctx context.Context, id string) error {
	c, err := b.Channel(ctx, id, true)
	if err != nil {
		return err
	}
	return Send(ctx, *c, Message{Severity: Info, Server: b.hostname, Title: "Test notification", Body: "If you can read this, the channel works.", Category: "system", Time: time.Now(), Link: b.panelURL + "/notifications"})
}

// Events lists recent events newest first.
func (b *Bus) Events(ctx context.Context, limit int, before int64) ([]Event, error) {
	q := `SELECT id, category, severity, title, message, link, created_at FROM events WHERE server_id = ?`
	args := []any{b.st.ServerID}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := b.st.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Category, &e.Severity, &e.Title, &e.Message, &e.Link, &e.CreatedAt); err == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// Delivery is the status of one event on one channel.
type Delivery struct {
	EventID   int64  `json:"eventId"`
	ChannelID string `json:"channelId"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	Error     string `json:"error"`
	SentAt    string `json:"sentAt,omitempty"`
}

// Deliveries lists delivery rows for an event.
func (b *Bus) Deliveries(ctx context.Context, eventID int64) ([]Delivery, error) {
	rows, err := b.st.DB.QueryContext(ctx, `SELECT event_id, channel_id, status, attempts, error, sent_at FROM deliveries WHERE event_id = ? ORDER BY id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		var sent sql.NullString
		if err := rows.Scan(&d.EventID, &d.ChannelID, &d.Status, &d.Attempts, &d.Error, &sent); err == nil {
			d.SentAt = sent.String
			out = append(out, d)
		}
	}
	return out, nil
}

// Purge removes events older than the retention window.
func (b *Bus) Purge(ctx context.Context, keep time.Duration) {
	cut := time.Now().Add(-keep).UTC().Format(sqlTime)
	_, _ = b.st.DB.ExecContext(ctx, `DELETE FROM events WHERE server_id = ? AND created_at < ?`, b.st.ServerID, cut)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
