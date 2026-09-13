package api

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/isletdev/islet/pkg/api"
)

var (
	scoreMu   sync.Mutex
	scoreAt   time.Time
	scoreVal  int
	scoreFail int
)

// securityScore returns the cached score, refreshing it at most every five
// minutes.
//
// The sweep shells out a dozen times and takes seconds, so it runs outside the
// lock: holding it would block every other dashboard poll. It also runs on its
// own context, because the score used to be computed from commands that were
// killed when the first client disconnected, and the wrong number was then
// cached for five minutes.
func (s *Server) securityScore(ctx context.Context) (int, int) {
	scoreMu.Lock()
	fresh := time.Since(scoreAt) <= 5*time.Minute
	val, fail := scoreVal, scoreFail
	scoreMu.Unlock()
	if fresh || s.security == nil {
		return val, fail
	}

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	rep := s.security.Report(cctx)
	if cctx.Err() != nil {
		return val, fail // timed out; keep the last good number
	}
	failing := 0
	for _, c := range rep.Checks {
		if c.Status == "fail" {
			failing++
		}
	}
	scoreMu.Lock()
	scoreVal, scoreFail, scoreAt = rep.Score, failing, time.Now()
	scoreMu.Unlock()
	return rep.Score, failing
}

// handleAttention aggregates what the dashboard should surface: security
// score (cached five minutes), backup health, checks down, failed deploys
// and jobs, and recent critical events.
func (s *Server) handleAttention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{}
	out["securityScore"], out["securityFailing"] = s.securityScore(ctx)
	if s.backup != nil {
		out["backups"] = s.backup.Health(ctx)
	}
	if s.uptime != nil {
		down := []string{}
		if checks, err := s.uptime.List(ctx); err == nil {
			for _, c := range checks {
				if c.Enabled && c.Status == "down" {
					down = append(down, c.Name)
				}
			}
		}
		out["checksDown"] = down
	}
	if s.deploy != nil {
		failed := []string{}
		if apps, err := s.deploy.List(ctx); err == nil {
			for _, a := range apps {
				if a.Last != nil && a.Last.Status == "failed" {
					failed = append(failed, a.Name)
				}
			}
			out["apps"] = len(apps)
		}
		out["deploysFailed"] = failed
	}
	if s.cron != nil {
		failed := []string{}
		if jobs, err := s.cron.List(ctx); err == nil {
			for _, j := range jobs {
				if j.Enabled && (j.Overdue || (j.LastRun != nil && (j.LastRun.Status == "failed" || j.LastRun.Status == "timeout"))) {
					failed = append(failed, j.Name)
				}
			}
		}
		out["jobsFailed"] = failed
	}
	if s.notify != nil {
		if evs, err := s.notify.Events(ctx, 20, 0); err == nil {
			crit := []map[string]string{}
			for _, e := range evs {
				if e.Severity == "critical" && time.Since(parseSQLTime(e.CreatedAt)) < 24*time.Hour {
					crit = append(crit, map[string]string{"title": e.Title, "at": e.CreatedAt, "link": e.Link})
				}
			}
			out["criticals"] = crit
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func parseSQLTime(s string) time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.000Z", s)
	return t
}

var dialer = &net.Dialer{Timeout: 5 * time.Second}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,253}$`)

// handleDiagnostics streams ping, traceroute, dig or a port check.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tool, host := q.Get("tool"), q.Get("host")
	if !hostRe.MatchString(host) {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "host must be a hostname or IP"})
		return
	}
	var argv []string
	switch tool {
	case "ping":
		if runtime.GOOS == "windows" {
			argv = []string{"ping", "-n", "4", host}
		} else {
			argv = []string{"ping", "-c", "4", "-W", "2", host}
		}
	case "traceroute":
		if runtime.GOOS == "windows" {
			argv = []string{"tracert", "-d", "-w", "1000", "-h", "20", host}
		} else if _, err := exec.LookPath("traceroute"); err == nil {
			argv = []string{"traceroute", "-n", "-w", "2", "-m", "20", host}
		} else {
			argv = []string{"tracepath", "-n", "-m", "20", host}
		}
	case "dig":
		if _, err := exec.LookPath("dig"); err == nil {
			argv = []string{"dig", "+noall", "+answer", "+stats", host, "A", host, "AAAA", host, "MX", host, "TXT"}
		} else {
			argv = []string{"nslookup", "-type=any", host}
		}
	case "port":
		port, err := strconv.Atoi(q.Get("port"))
		if err != nil || port < 1 || port > 65535 {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "port must be 1-65535"})
			return
		}
		cctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		start := time.Now()
		conn, err := dialer.DialContext(cctx, "tcp", host+":"+strconv.Itoa(port))
		msg := "closed or filtered: " + errString(err)
		if err == nil {
			conn.Close()
			msg = "open, connected in " + time.Since(start).Round(time.Millisecond).String()
		}
		writeJSON(w, http.StatusOK, map[string]any{"open": err == nil, "message": msg})
		return
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "tool must be ping, traceroute, dig or port"})
		return
	}
	rc, wait, err := s.runner.Stream(r.Context(), userFrom(r.Context()).Username, argv[0], argv[1:]...)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "tool", Message: err.Error()})
		return
	}
	streamLines(w, r, rc, wait)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
