package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/notify"
)

// StartWeeklyReport emits a "your server this week" event every Monday
// morning. Channels that accept the report category (email is the natural
// fit) deliver it; the panel timeline keeps it either way.
func (s *Server) StartWeeklyReport(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			now := time.Now()
			if now.Weekday() != time.Monday || now.Hour() < 8 || now.Hour() > 10 {
				continue
			}
			last, _, _ := s.store.Setting(ctx, "report.last_weekly")
			if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < 6*24*time.Hour {
				continue
			}
			if v, _, _ := s.store.Setting(ctx, "report.weekly"); v == "off" {
				continue
			}
			body := s.weeklyReport(ctx)
			if s.notify != nil {
				s.notify.Emit(ctx, notify.Event{Category: "report", Severity: notify.Info, Title: "Your server this week: " + s.store.Hostname, Message: body, Link: "/"})
			}
			_ = s.store.SetSetting(ctx, "report.last_weekly", now.UTC().Format(time.RFC3339))
		}
	}()
}

// weeklyReport builds the plain-text summary.
func (s *Server) weeklyReport(ctx context.Context) string {
	var b strings.Builder
	since := time.Now().Add(-7 * 24 * time.Hour)
	if s.sampler != nil {
		if l := s.sampler.Latest(); l != nil && l.DiskTotal > 0 {
			fmt.Fprintf(&b, "Disk: %.0f%% used (%s of %s)\n", float64(l.DiskUsed)/float64(l.DiskTotal)*100, humanBytes(l.DiskUsed), humanBytes(l.DiskTotal))
		}
	}
	if s.security != nil {
		rep := s.security.Report(ctx)
		fails := 0
		for _, c := range rep.Checks {
			if c.Status == "fail" {
				fails++
			}
		}
		fmt.Fprintf(&b, "Security Score: %d/100 (%d to fix)\n", rep.Score, fails)
	}
	if s.deploy != nil {
		apps, _ := s.deploy.List(ctx)
		ok, failed := 0, 0
		for _, a := range apps {
			rels, _ := s.deploy.Releases(ctx, a.ID, 50)
			for _, r := range rels {
				if t, err := time.Parse(time.RFC3339, strings.Replace(r.StartedAt, "Z", "+00:00", 1)); err == nil && t.Before(since) {
					continue
				}
				if r.Status == "failed" {
					failed++
				} else if r.Status == "live" || r.Status == "superseded" {
					ok++
				}
			}
		}
		fmt.Fprintf(&b, "Deploys: %d apps, %d successful and %d failed releases this week\n", len(apps), ok, failed)
	}
	if s.uptime != nil {
		checks, _ := s.uptime.List(ctx)
		down := []string{}
		var sum float64
		n := 0
		for _, c := range checks {
			if c.Status == "down" {
				down = append(down, c.Name)
			}
			if c.Uptime30d >= 0 {
				sum += c.Uptime30d
				n++
			}
		}
		if n > 0 {
			fmt.Fprintf(&b, "Uptime: %d checks, %.2f%% average over 30 days", n, sum/float64(n))
			if len(down) > 0 {
				fmt.Fprintf(&b, ", down now: %s", strings.Join(down, ", "))
			}
			b.WriteString("\n")
		}
	}
	if s.cron != nil {
		jobs, _ := s.cron.List(ctx)
		failed := []string{}
		for _, j := range jobs {
			if j.LastRun != nil && (j.LastRun.Status == "failed" || j.LastRun.Status == "timeout") {
				failed = append(failed, j.Name)
			}
		}
		fmt.Fprintf(&b, "Cron: %d jobs", len(jobs))
		if len(failed) > 0 {
			fmt.Fprintf(&b, ", failing: %s", strings.Join(failed, ", "))
		}
		b.WriteString("\n")
	}
	if s.backup != nil {
		h := s.backup.Health(ctx)
		if plans, _ := h["plans"].(int); plans == 0 {
			b.WriteString("Backups: no plan configured\n")
		} else {
			fmt.Fprintf(&b, "Backups: %d plans, last success %v, %v stale, %v failed, %s stored\n", plans, orNever(h["lastSuccess"]), h["stale"], h["failed"], humanBytes(uint64(toInt64(h["size"]))))
		}
	}
	if s.docker != nil {
		if list, err := s.docker.Containers(ctx, "system"); err == nil {
			running := 0
			for _, c := range list {
				if c.State == "running" {
					running++
				}
			}
			fmt.Fprintf(&b, "Containers: %d running of %d\n", running, len(list))
		}
	}
	return strings.TrimSpace(b.String())
}

func orNever(v any) string {
	if s, _ := v.(string); s != "" {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", s); err == nil {
			return t.Local().Format("Jan 2 15:04")
		}
		return s
	}
	return "never"
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func humanBytes(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + " " + units[i]
}

// handleReportSetting reads or writes the weekly report toggle.
func (s *Server) handleReportSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		v, _, _ := s.store.Setting(r.Context(), "report.weekly")
		last, _, _ := s.store.Setting(r.Context(), "report.last_weekly")
		writeJSON(w, http.StatusOK, map[string]any{"enabled": v != "off", "lastSent": last})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError("bad_json", err.Error()))
		return
	}
	v := "on"
	if !req.Enabled {
		v = "off"
	}
	if err := s.store.SetSetting(r.Context(), "report.weekly", v); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError("internal", err.Error()))
		return
	}
	last, _, _ := s.store.Setting(r.Context(), "report.last_weekly")
	writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled, "lastSent": last})
}

// handleReportSend emits the weekly report right now (preview / test).
func (s *Server) handleReportSend(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	body := s.weeklyReport(r.Context())
	if s.notify != nil {
		s.notify.Emit(r.Context(), notify.Event{Category: "report", Severity: notify.Info, Title: "Your server this week: " + s.store.Hostname, Message: body, Link: "/"})
	}
	writeJSON(w, http.StatusOK, map[string]string{"body": body})
}
