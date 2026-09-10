// Package watch turns what the daemon observes into events on the bus:
// container crashes, resource thresholds, certificate expiry and update
// availability. Each watcher is a goroutine started from main.
package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/metrics"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/update"
	"github.com/isletdev/islet/internal/version"
)

// Docker follows `docker events` and reports containers that die with a
// non-zero exit, OOM kills, and restart loops.
func Docker(ctx context.Context, run *cmdrun.Runner, bus *notify.Bus, log *slog.Logger) {
	dies := map[string][]time.Time{}
	var mu sync.Mutex
	for ctx.Err() == nil {
		rc, wait, err := run.Stream(ctx, "system", "docker", "events", "--filter", "type=container", "--format", "{{json .}}")
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var ev struct {
				Action string `json:"Action"`
				Actor  struct {
					Attributes map[string]string `json:"Attributes"`
				} `json:"Actor"`
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			name := ev.Actor.Attributes["name"]
			if name == proxy.ContainerName && ev.Action != "die" && ev.Action != "oom" {
				continue
			}
			switch ev.Action {
			case "oom":
				bus.Emit(ctx, notify.Event{Category: "container", Severity: notify.Critical, Title: "Container out of memory: " + name, Message: "The kernel killed a process in " + name + " for exceeding its memory limit.", Link: "/containers"})
			case "die":
				code := ev.Actor.Attributes["exitCode"]
				if code == "0" {
					continue
				}
				mu.Lock()
				now := time.Now()
				h := append(dies[name], now)
				var recent []time.Time
				for _, t := range h {
					if now.Sub(t) < 10*time.Minute {
						recent = append(recent, t)
					}
				}
				dies[name] = recent
				n := len(recent)
				mu.Unlock()
				if n == 1 {
					bus.Emit(ctx, notify.Event{Category: "container", Severity: notify.Warning, Title: "Container exited: " + name, Message: fmt.Sprintf("%s exited with code %s.", name, code), Link: "/containers"})
				} else if n == 3 {
					bus.Emit(ctx, notify.Event{Category: "container", Severity: notify.Critical, Title: "Container crash loop: " + name, Message: fmt.Sprintf("%s died %d times in ten minutes (last exit code %s).", name, n, code), Link: "/containers"})
				}
			}
		}
		rc.Close()
		_ = wait()
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// Resources watches the sampler for sustained pressure and disk usage.
func Resources(ctx context.Context, sampler *metrics.Sampler, bus *notify.Bus) {
	ch, cancel := sampler.Subscribe()
	defer cancel()
	var memHigh, diskHigh, cpuHigh time.Time
	memAlerted, diskAlerted, cpuAlerted := false, false, false
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-ch:
			now := time.Now()
			// Disk: alert at 85%, critical at 95%, recover below 80%.
			if s.DiskTotal > 0 {
				pct := float64(s.DiskUsed) / float64(s.DiskTotal) * 100
				switch {
				case pct >= 85 && !diskAlerted:
					if diskHigh.IsZero() {
						diskHigh = now
					} else if now.Sub(diskHigh) > 30*time.Second {
						sev := notify.Warning
						if pct >= 95 {
							sev = notify.Critical
						}
						bus.Emit(ctx, notify.Event{Category: "system", Severity: sev, Title: "Disk usage high", Message: fmt.Sprintf("Root filesystem is %.0f%% full (%s of %s). Open Disk Doctor to reclaim space.", pct, human(s.DiskUsed), human(s.DiskTotal)), Link: "/"})
						diskAlerted = true
					}
				case pct < 80 && diskAlerted:
					bus.Emit(ctx, notify.Event{Category: "system", Severity: notify.Info, Title: "Disk usage high", Message: fmt.Sprintf("Recovered: root filesystem is back to %.0f%%.", pct), Link: "/"})
					diskAlerted, diskHigh = false, time.Time{}
				case pct < 85:
					diskHigh = time.Time{}
				}
			}
			// Memory: 92% for two minutes.
			if s.MemTotal > 0 {
				pct := float64(s.MemUsed) / float64(s.MemTotal) * 100
				memAlerted, memHigh = sustained(ctx, bus, "Memory pressure", "system", pct, 92, 85, 2*time.Minute, memAlerted, memHigh, now, "%.0f%% of memory in use. Check the top processes on the overview.")
			}
			// CPU: 95% for five minutes.
			cpuAlerted, cpuHigh = sustained(ctx, bus, "CPU saturated", "system", s.CPUPct, 95, 80, 5*time.Minute, cpuAlerted, cpuHigh, now, "CPU has been at %.0f%% for five minutes.")
		}
	}
}

func sustained(ctx context.Context, bus *notify.Bus, title, cat string, pct, hi, lo float64, hold time.Duration, alerted bool, since time.Time, now time.Time, msg string) (bool, time.Time) {
	switch {
	case pct >= hi && !alerted:
		if since.IsZero() {
			return false, now
		}
		if now.Sub(since) >= hold {
			bus.Emit(ctx, notify.Event{Category: cat, Severity: notify.Warning, Title: title, Message: fmt.Sprintf(msg, pct), Link: "/"})
			return true, since
		}
		return false, since
	case pct < lo && alerted:
		bus.Emit(ctx, notify.Event{Category: cat, Severity: notify.Info, Title: title, Message: fmt.Sprintf("Recovered: now at %.0f%%.", pct), Link: "/"})
		return false, time.Time{}
	case pct < hi:
		return alerted, time.Time{}
	}
	return alerted, since
}

// Daily runs once a day: certificate expiry and update availability.
func Daily(ctx context.Context, px *proxy.Manager, bus *notify.Bus, log *slog.Logger) {
	run := func() {
		if certs, err := px.Certificates(); err == nil {
			for _, c := range certs {
				left := time.Until(c.NotAfter)
				switch {
				case left < 0:
					bus.Emit(ctx, notify.Event{Category: "domain", Severity: notify.Critical, Title: "Certificate expired: " + c.Domain, Message: "Renewal has been failing. Check that DNS still points here and port 80 is reachable.", Link: "/domains"})
				case left < 7*24*time.Hour:
					bus.Emit(ctx, notify.Event{Category: "domain", Severity: notify.Warning, Title: "Certificate expiring: " + c.Domain, Message: fmt.Sprintf("Expires in %d days and has not renewed yet.", int(left.Hours()/24)), Link: "/domains"})
				}
			}
		}
		if rel, err := update.Latest(ctx, update.Stable); err == nil && update.IsNewer(rel.Version, version.Version) {
			bus.Emit(ctx, notify.Event{Category: "system", Severity: notify.Info, Title: "Islet update available", Message: fmt.Sprintf("%s is available (you run %s). Install it from Settings.", rel.Version, version.Version), Link: "/settings"})
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
		run()
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func human(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + " " + units[i]
}
