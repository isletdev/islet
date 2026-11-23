package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"strings"
	"time"
)

// Maintenance is the switch that takes every site on the server down at once.
//
// It is the per-domain maintenance flag applied to all of them, not a second
// mechanism: Reconcile marks each domain before rendering, so the routers,
// locations and the page they land on are the ones a single domain in
// maintenance already uses. The panel's own domains are left out, or the
// switch would lock out the person who has to turn it off again.
//
// Kept in settings rather than a table: it is one value, and a migration for
// it would buy nothing a JSON setting does not.
type Maintenance struct {
	On      bool   `json:"on"`
	Message string `json:"message"`
	// Until is when the work is expected to end, RFC 3339, or empty. It is
	// shown to visitors and sent as Retry-After; nothing switches off by
	// itself when it passes, since work that overruns is the usual case.
	Until     string `json:"until"`
	ChangedAt string `json:"changedAt,omitempty"`
	ChangedBy string `json:"changedBy,omitempty"`
}

const keyMaintenance = "proxy.maintenance"

// DefaultMaintenanceMessage is what visitors read when no message was set.
const DefaultMaintenanceMessage = "This site is down for scheduled maintenance and will be back shortly."

// Maintenance reads the server-wide switch.
func (m *Manager) Maintenance(ctx context.Context) Maintenance {
	var mt Maintenance
	if v, _, _ := m.st.Setting(ctx, keyMaintenance); v != "" {
		_ = json.Unmarshal([]byte(v), &mt)
	}
	return mt
}

// Validate normalises the switch and says what is wrong with it.
func (mt *Maintenance) Validate() error {
	mt.Message = strings.TrimSpace(mt.Message)
	mt.Until = strings.TrimSpace(mt.Until)
	if len(mt.Message) > 500 {
		return errors.New("the message is longer than 500 characters")
	}
	if mt.Until != "" {
		t, err := time.Parse(time.RFC3339, mt.Until)
		if err != nil {
			return errors.New("the end time must be a date and time")
		}
		mt.Until = t.UTC().Format(time.RFC3339)
	}
	return nil
}

// SetMaintenance stores the switch and applies it to the proxy.
func (m *Manager) SetMaintenance(ctx context.Context, actor string, mt Maintenance) (Maintenance, error) {
	if err := mt.Validate(); err != nil {
		return mt, err
	}
	prev := m.Maintenance(ctx)
	mt.ChangedAt, mt.ChangedBy = prev.ChangedAt, prev.ChangedBy
	if mt.On != prev.On {
		mt.ChangedAt, mt.ChangedBy = time.Now().UTC().Format(time.RFC3339), actor
	}
	b, _ := json.Marshal(mt)
	if err := m.st.SetSetting(ctx, keyMaintenance, string(b)); err != nil {
		return mt, err
	}
	detail := "off"
	if mt.On {
		detail = "on"
	}
	_ = m.st.Audit(ctx, actor, "proxy.maintenance", "all sites", detail)
	return mt, m.Reconcile(ctx)
}

// underMaintenance marks every domain but the panel's when the switch is on.
func underMaintenance(domains []Domain, on bool) {
	if !on {
		return
	}
	for i := range domains {
		if domains[i].TargetType != "panel" {
			domains[i].Maintenance = true
		}
	}
}

// RetryAfter is how many seconds a client should wait before asking again.
func (mt Maintenance) RetryAfter(now time.Time) int {
	if t, err := time.Parse(time.RFC3339, mt.Until); err == nil && t.After(now) {
		return min(int(t.Sub(now).Seconds())+1, 86400)
	}
	return 120
}

var maintenanceTmpl = template.Must(template.New("m").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Down for maintenance</title>
<style>:root{color-scheme:light dark;--bg:#fafafa;--ink:#0a0a0a;--muted:#525252}@media (prefers-color-scheme:dark){:root{--bg:#0a0a0a;--ink:#fafafa;--muted:#a3a3a3}}body{margin:0;font:16px/1.55 system-ui,sans-serif;background:var(--bg);color:var(--ink);display:grid;place-items:center;min-height:100vh}main{max-width:32rem;padding:2rem 1rem}h1{font-size:1.5rem;margin:0 0 .5rem;letter-spacing:-.01em}p{color:var(--muted);margin:0 0 .5rem;white-space:pre-line}time{color:var(--ink)}</style>
<main><h1>Down for maintenance</h1><p>{{.Message}}</p>{{if .Until}}<p>Expected back by <time datetime="{{.Until}}">{{.UntilText}}</time>.</p>{{end}}</main></html>`))

// MaintenanceHTML is the page a visitor's browser gets.
func MaintenanceHTML(mt Maintenance) []byte {
	msg := mt.Message
	if msg == "" {
		msg = DefaultMaintenanceMessage
	}
	var until, text string
	if t, err := time.Parse(time.RFC3339, mt.Until); err == nil && t.After(time.Now()) {
		until, text = mt.Until, t.UTC().Format("15:04 MST, 2 January 2006")
	}
	var b strings.Builder
	_ = maintenanceTmpl.Execute(&b, map[string]string{"Message": msg, "Until": until, "UntilText": text})
	return []byte(b.String())
}

// MaintenanceJSON is what an API client gets instead.
func MaintenanceJSON(mt Maintenance) []byte {
	msg := mt.Message
	if msg == "" {
		msg = DefaultMaintenanceMessage
	}
	body := map[string]string{"error": "maintenance", "message": msg}
	if t, err := time.Parse(time.RFC3339, mt.Until); err == nil && t.After(time.Now()) {
		body["until"] = mt.Until
	}
	b, _ := json.Marshal(body)
	return append(b, '\n')
}
