package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/uptime"
	"github.com/isletdev/islet/pkg/api"
)

// The one page this panel serves to people who are not the operator.
//
// Everything else here is behind a session and describes a server to the person
// who runs it. A status page is for the customer wondering whether it is them or
// the site, which makes it a different thing with different rules: no
// authentication, no JavaScript, no framework, and nothing on it that the
// operator has not deliberately put there. A check's target in particular never
// reaches this file — the page says "Website" and whether it answered, not which
// URL was probed.
//
// Rendered on the server rather than by the panel's React app, for the same
// reason: the app is the operator's tool, it is 400 KB, and it is behind a
// login. This is one request, one template, no build step and no bytes a
// stranger has to trust.

const statusSettingKey = "status.page"

// StatusSettings is what the operator chose.
type StatusSettings struct {
	Enabled bool `json:"enabled"`
	// Title is what the page calls itself: a company, usually.
	Title string `json:"title"`
	// Host serves the page on a hostname of its own, the way the media service
	// does. Empty leaves it at /status on whatever address the panel answers.
	Host string `json:"host"`
	// Message is a line under the heading, for whatever the operator wants to
	// say — maintenance tonight, where to write in.
	Message string `json:"message"`
}

func (s *Server) statusSettings(ctx context.Context) StatusSettings {
	var st StatusSettings
	raw, ok, err := s.store.Setting(ctx, statusSettingKey)
	if err != nil || !ok {
		return st
	}
	_ = json.Unmarshal([]byte(raw), &st)
	return st
}

// handleStatusSettings is the operator's half: what the page says and whether
// it is on at all.
func (s *Server) handleStatusSettings(w http.ResponseWriter, r *http.Request) {
	if s.uptime == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Error{Error: "unavailable", Message: "uptime is not available on this daemon"})
		return
	}
	if r.Method == http.MethodPost {
		if !s.adminOnly(w, r) {
			return
		}
		var req StatusSettings
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		req.Host = strings.ToLower(strings.TrimSpace(req.Host))
		req.Title = strings.TrimSpace(req.Title)
		actor := userFrom(r.Context()).Username
		// The domain first, because it is the half that can be refused. The
		// helper is the media service's: "make a domain for this host, pointed
		// at the panel, and refuse if it already belongs to an application" is
		// the same question, and answering it twice in two places is how the
		// two answers drift apart.
		if err := s.mediaRoute(r.Context(), actor, req.Host); err != nil {
			writeJSON(w, http.StatusConflict, api.Error{Error: "domain", Message: err.Error()})
			return
		}
		body, err := json.Marshal(req)
		if err != nil {
			s.failed(w, "status", err)
			return
		}
		if err := s.store.SetSetting(r.Context(), statusSettingKey, string(body)); err != nil {
			s.failed(w, "status", err)
			return
		}
		_ = s.store.Audit(r.Context(), actor, "status.settings", "status page", boolWord(req.Enabled))
	}
	set := s.statusSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": set,
		// Whether a domain for that host actually points here. Naming a host
		// tells this daemon to answer on it; it does not tell the proxy the
		// host exists, and the answer to that is the proxy's own 404 — which
		// reads as a broken status page and is really a missing domain.
		"hostRouted": s.mediaHostRouted(r.Context(), set.Host),
		"path":       "/status",
	})
}

// handleStatusPage is the page itself, to anybody who asks.
func (s *Server) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	set := s.statusSettings(r.Context())
	if !set.Enabled || s.uptime == nil {
		// Off is a 404 rather than a message: a status page nobody has turned
		// on should not tell a stranger that this server has one.
		http.NotFound(w, r)
		return
	}
	st, err := s.uptime.Public(r.Context())
	if err != nil {
		s.log.Warn("status page: could not read the checks", "err", err)
		http.Error(w, "the status page is not available", http.StatusInternalServerError)
		return
	}
	title := set.Title
	if title == "" {
		title = "Status"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Nothing here is private, and a status page under load is a status page
	// during an incident: a minute of cache is the difference between a page
	// that holds up and a server that is being asked for its own uptime a
	// thousand times a second while it is already struggling.
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// No scripts, no styles from anywhere, no frames: the whole page is below.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'")
	if err := statusTemplate.Execute(w, statusView(title, set.Message, st)); err != nil {
		s.log.Warn("status page: could not render", "err", err)
	}
}

type statusPageView struct {
	Title    string
	Message  string
	Headline string
	AllWell  bool
	Checks   []statusCheckView
	Updated  string
}

type statusCheckView struct {
	Name    string
	State   string
	Detail  string
	Uptime  string
	Days    []statusDayView
	Healthy bool
}

type statusDayView struct {
	Title string
	Class string
}

func statusView(title, message string, st uptime.PublicStatus) statusPageView {
	v := statusPageView{Title: title, Message: message, AllWell: st.Down == 0}
	switch {
	case len(st.Checks) == 0:
		v.Headline = "Nothing is being reported yet"
		v.AllWell = false
	case st.Down == 0:
		v.Headline = "All systems operational"
	case st.Down == 1:
		v.Headline = "One system is down"
	default:
		v.Headline = fmt.Sprintf("%d systems are down", st.Down)
	}
	if st.CheckedAt != "" {
		if t, err := time.Parse(time.RFC3339, st.CheckedAt); err == nil {
			v.Updated = t.UTC().Format("2 January 2006, 15:04 UTC")
		}
	}
	for _, c := range st.Checks {
		cv := statusCheckView{Name: c.Name, Healthy: c.Status == "up"}
		switch c.Status {
		case "up":
			cv.State = "Operational"
			if c.LastLatency > 0 {
				cv.Detail = fmt.Sprintf("%d ms", c.LastLatency)
			}
		case "down":
			cv.State = "Down"
			if t, err := time.Parse(time.RFC3339, c.DownSince); err == nil {
				cv.Detail = "since " + t.UTC().Format("15:04 UTC on 2 January")
			}
		case "paused":
			cv.State = "Paused"
		default:
			cv.State = "No data yet"
		}
		// The figure is added up from the same days the bars are drawn from, so
		// the two cannot disagree — and a check with no history at all gets no
		// figure rather than "0.00% over 30 days", which is what a month of
		// total failure looks like and the worst possible way for a page whose
		// whole job is to be believed to be wrong.
		var up, total int
		for _, d := range c.Days {
			up += d.Up
			total += d.Up + d.Down
		}
		if total > 0 {
			cv.Uptime = fmt.Sprintf("%.2f%% over 30 days", float64(up)/float64(total)*100)
		}
		for _, d := range c.Days {
			dv := statusDayView{Title: d.Date}
			switch {
			case d.Up == 0 && d.Down == 0:
				dv.Class, dv.Title = "none", d.Date+" — no data"
			case d.Down == 0:
				dv.Class, dv.Title = "up", d.Date+" — no failures"
			case d.Up == 0:
				dv.Class, dv.Title = "down", fmt.Sprintf("%s — down all day", d.Date)
			default:
				dv.Class, dv.Title = "partial", fmt.Sprintf("%s — %d of %d checks failed", d.Date, d.Down, d.Up+d.Down)
			}
			cv.Days = append(cv.Days, dv)
		}
		v.Checks = append(v.Checks, cv)
	}
	return v
}

// One file, no build step, and colour is never the only thing that says a
// system is down — the word is beside it, and each day carries its own title.
var statusTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: light dark; --bg:#fbfbf9; --card:#fff; --ink:#111; --muted:#5a6570; --line:#e5e4de; --up:#2f7d62; --down:#b4442e; --none:#d8d7d1; }
  @media (prefers-color-scheme: dark) { :root { --bg:#0f1316; --card:#161b1f; --ink:#f3f3f1; --muted:#9aa5ad; --line:#242a2f; --none:#2b3136; } }
  * { box-sizing: border-box; }
  body { margin:0; background:var(--bg); color:var(--ink); font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif; }
  main { max-width:44rem; margin:0 auto; padding:3rem 1rem 4rem; }
  h1 { font-size:1.35rem; margin:0 0 .25rem; }
  .banner { margin:1.5rem 0 2rem; padding:1rem 1.25rem; border-radius:12px; border:1px solid var(--line); background:var(--card); }
  .banner.good { border-left:4px solid var(--up); }
  .banner.bad { border-left:4px solid var(--down); }
  .banner p { margin:0; font-weight:600; }
  .muted { color:var(--muted); font-size:.875rem; }
  ul { list-style:none; margin:0; padding:0; }
  li.check { padding:1rem 1.25rem; border:1px solid var(--line); border-radius:12px; background:var(--card); margin-bottom:.75rem; }
  .row { display:flex; flex-wrap:wrap; gap:.5rem 1rem; align-items:baseline; justify-content:space-between; }
  .name { font-weight:600; }
  .state.up { color:var(--up); font-weight:600; }
  .state.down { color:var(--down); font-weight:600; }
  .bars { display:flex; gap:2px; margin-top:.75rem; }
  .bars span { flex:1; height:26px; border-radius:2px; background:var(--none); }
  .bars span.up { background:var(--up); }
  .bars span.down { background:var(--down); }
  .bars span.partial { background:linear-gradient(to bottom, var(--up) 55%, var(--down) 55%); }
  footer { margin-top:2rem; text-align:center; }
</style>
</head>
<body>
<main>
  <h1>{{.Title}}</h1>
  {{if .Message}}<p class="muted">{{.Message}}</p>{{end}}
  <div class="banner {{if .AllWell}}good{{else}}bad{{end}}">
    <p>{{.Headline}}</p>
    {{if .Updated}}<p class="muted">Last checked {{.Updated}}</p>{{end}}
  </div>
  <ul>
  {{range .Checks}}
    <li class="check">
      <div class="row">
        <span class="name">{{.Name}}</span>
        <span class="state {{if .Healthy}}up{{else}}down{{end}}">{{.State}}{{if .Detail}} <span class="muted">· {{.Detail}}</span>{{end}}</span>
      </div>
      <div class="bars" role="img" aria-label="The last 30 days for {{.Name}}">
        {{range .Days}}<span class="{{.Class}}" title="{{.Title}}"></span>{{end}}
      </div>
      {{if .Uptime}}<p class="muted" style="margin:.5rem 0 0">{{.Uptime}}</p>{{end}}
    </li>
  {{end}}
  </ul>
  <footer class="muted">Thirty days shown. Times are UTC.</footer>
</main>
</body>
</html>
`))
