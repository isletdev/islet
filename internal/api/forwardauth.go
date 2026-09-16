package api

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// forwardAuthPath is the route Traefik asks on behalf of a protected site.
const forwardAuthPath = "/_islet/auth"

func apiError(code, msg string) api.Error { return api.Error{Error: code, Message: msg} }

// cookieDomain is the parent domain the session cookie is scoped to so a
// panel session also authenticates protected app routes ("" = host only).
var cookieDomain atomic.Value

func (s *Server) loadCookieDomain() {
	v, _, _ := s.store.Setting(contextBackground(), "auth.cookie_domain")
	cookieDomain.Store(strings.ToLower(strings.TrimSpace(v)))
}

func currentCookieDomain(r *http.Request) string {
	d, _ := cookieDomain.Load().(string)
	if d == "" {
		return ""
	}
	host := strings.ToLower(r.Host)
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	if host == d || strings.HasSuffix(host, "."+d) {
		return d
	}
	return ""
}

// handleForwardAuth answers Traefik's forwardAuth for protected routes.
//
// A valid, MFA-complete panel session passes with X-Islet-User; anything else
// is sent to the panel login with a return address. When the rule names
// particular people — the u parameter, written into the middleware's address
// by the render path and never taken from the visitor — a session belonging to
// somebody else is refused rather than redirected: sending them to a login
// they have already completed would only loop.
func (s *Server) handleForwardAuth(w http.ResponseWriter, r *http.Request) {
	site := r.Header.Get("X-Forwarded-Host")
	if u := s.gateUser(r); u != nil {
		if !userAllowed(r.URL.Query().Get("u"), u.Username) {
			s.denyProtected(w, r, u.Username, site)
			return
		}
		w.Header().Set("X-Islet-User", u.Username)
		w.Header().Set("X-Islet-Role", u.Role)
		w.WriteHeader(http.StatusOK)
		return
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "https"
	}
	next := proto + "://" + site + r.Header.Get("X-Forwarded-Uri")
	panel := s.panelURL(r.Context())
	if panel == "" {
		http.Error(w, "sign in to the Islet panel first", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, panel+"/login?next="+url.QueryEscape(next), http.StatusFound)
}

// userAllowed reports whether a rule's allow-list admits this account.
//
// An empty list means any signed-in Islet user. That is deliberate and is what
// the single protect switch meant before lists existed, so a rule saved by an
// older panel — or by an MCP client that sends only {"protect": true} — keeps
// working rather than turning into a site nobody can reach.
func userAllowed(list, username string) bool {
	allowed := splitUsers(list)
	return len(allowed) == 0 || slices.Contains(allowed, strings.ToLower(username))
}

// gateUser is who is asking, as far as the gate is concerned.
//
// The panel's own session cookie is host-only and will not be here: what the
// browser sent to the protected site is the gate cookie, which is why it
// exists. An API token still identifies its owner, and a session cookie is
// honoured for the case where the panel and the protected route share a host.
func (s *Server) gateUser(r *http.Request) *auth.User {
	if u := userFrom(r.Context()); u != nil {
		return u
	}
	c, err := r.Cookie(gateCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := s.auth.SessionByGate(r.Context(), c.Value)
	if err != nil || sess.MFAPending {
		return nil
	}
	u, err := s.auth.UserByID(r.Context(), sess.UserID)
	if err != nil {
		return nil
	}
	return u
}

// splitUsers reads an allow-list as the render path wrote it.
func splitUsers(s string) []string {
	var out []string
	for _, u := range strings.Split(s, ",") {
		if u = strings.ToLower(strings.TrimSpace(u)); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// panelCache holds the answer for a moment, because every refused request asks
// for it. A bot on a protected site produces one of these per request, and the
// lookup reads every domain and every location on the server; a name the panel
// answers to does not change between two of them.
var panelCache struct {
	sync.Mutex
	url string
	at  time.Time
}

// panelURL is where a visitor is sent to sign in, or "" if the panel has no
// name of its own and the server has no public address either.
func (s *Server) panelURL(ctx context.Context) string {
	panelCache.Lock()
	defer panelCache.Unlock()
	if time.Since(panelCache.at) < 30*time.Second {
		return panelCache.url
	}
	panel := ""
	if doms, err := s.proxy.Domains(ctx); err == nil {
		for _, d := range doms {
			if d.TargetType == "panel" && d.Enabled {
				scheme := "https"
				if d.TLS == "none" {
					scheme = "http"
				}
				panel = scheme + "://" + d.Host
				if d.TLS != "none" {
					break
				}
			}
		}
	}
	if panel == "" {
		if ip := proxy.PublicIP(ctx); ip != "" {
			panel = "https://" + ip + ":9443"
		}
	}
	panelCache.url, panelCache.at = panel, time.Now()
	return panel
}

// deniedRecently keeps one refusal per person per site per minute out of the
// audit log. A browser refused on a page fetches its stylesheet, its script and
// its favicon and is refused for each of them; a bot is refused for as long as
// it keeps trying. Every one of those is the same event, and writing them all
// buries the one that matters under thousands that do not.
var denied struct {
	sync.Mutex
	seen map[string]time.Time
}

func firstDenialIn(window time.Duration, key string) bool {
	denied.Lock()
	defer denied.Unlock()
	now := time.Now()
	if denied.seen == nil {
		denied.seen = map[string]time.Time{}
	}
	if len(denied.seen) > 1000 {
		for k, t := range denied.seen {
			if now.Sub(t) > window {
				delete(denied.seen, k)
			}
		}
	}
	if t, ok := denied.seen[key]; ok && now.Sub(t) < window {
		return false
	}
	denied.seen[key] = now
	return true
}

// denyProtected answers a signed-in visitor the rule does not name.
//
// A page rather than a bare 403: this is somebody with an Islet account
// looking at a site they expected to reach, and the two things they need are
// which account they are using — often the wrong one of two — and where the
// permission lives, so an admin can be asked for it by name.
func (s *Server) denyProtected(w http.ResponseWriter, r *http.Request, who, site string) {
	if firstDenialIn(time.Minute, who+"|"+site) {
		_ = s.store.Audit(r.Context(), who, "proxy.protect.denied", site, "")
	}
	link := ""
	if p := s.panelURL(r.Context()); p != "" {
		link = `<p class="l"><a href="` + html.EscapeString(p) + `/domains">Open the Islet panel</a></p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, deniedPage, html.EscapeString(site), html.EscapeString(who), link)
}

const deniedPage = `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Not allowed</title>
<style>body{margin:0;font:16px/1.5 system-ui,sans-serif;background:#0A0A0A;color:#FAFAFA;display:grid;place-items:center;min-height:100vh}main{max-width:32rem;padding:2rem}h1{font-size:1.5rem;margin:0 0 .5rem}p{color:#A3A3A3;margin:0 0 .75rem}code{color:#FAFAFA}a{color:#FAFAFA}</style>
<main><h1>Not allowed</h1>
<p><code>%s</code> is protected by Islet, and this account is not on its list.</p>
<p>Signed in as <code>%s</code>. An Islet admin can add you under Domains &rarr; Protection.</p>
%s</main>`

func (s *Server) handleCookieDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		d, _ := cookieDomain.Load().(string)
		writeJSON(w, http.StatusOK, map[string]string{"cookieDomain": d})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		CookieDomain string `json:"cookieDomain"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError("bad_json", err.Error()))
		return
	}
	d := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(req.CookieDomain, ".")))
	if d != "" && (strings.Contains(d, "/") || !strings.Contains(d, ".")) {
		writeJSON(w, http.StatusBadRequest, apiError("invalid", "cookie domain must be a parent domain like example.com"))
		return
	}
	// A browser refuses a cookie scoped to a name nobody owns — "co.uk",
	// "com.au" — and refuses it silently: the setting saves, the cookie is
	// never stored, and every protected site keeps asking for a login that
	// appears to have worked. Better to say no here than to be mysterious
	// later.
	if publicSuffix(d) {
		writeJSON(w, http.StatusBadRequest, apiError("invalid",
			d+" is a public suffix, not a domain anybody owns, and a browser will not store a cookie for it. Use the name you registered, such as example."+d+"."))
		return
	}
	if err := s.store.SetSetting(r.Context(), "auth.cookie_domain", d); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError("internal", err.Error()))
		return
	}
	old, _ := cookieDomain.Load().(string)
	cookieDomain.Store(d)

	// Hand this session a gate cookie at the new scope, rather than telling
	// somebody to sign out and back in. Changing the domain is nearly always
	// done *because* a protected site just turned them away, and without this
	// the next attempt is refused again, by the same session they are sitting
	// in, and the setting looks broken.
	//
	// The old scope is cleared first, or two gate cookies would linger and the
	// browser would offer both.
	if sess := sessionFrom(r.Context()); sess != nil {
		clearGateCookie(w, r, old)
		if gate, err := s.auth.EnsureGate(r.Context(), sess.ID); err == nil {
			setGateCookie(w, r, gate, sess.ExpiresAt)
		}
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "auth.cookie_domain", d, "")
	writeJSON(w, http.StatusOK, map[string]string{"cookieDomain": d})
}

// publicSuffix reports whether a name is the shared part of the namespace
// rather than somebody's domain, for the handful of shapes that actually come
// up when somebody scopes a session cookie.
//
// This is not the public suffix list. That list is thousands of entries, it
// changes, and carrying it would mean a dependency and a periodic update for a
// check that runs when an admin types a domain into a settings field. What it
// catches is the case that occurs in practice: a two-label name ending in a
// country code, where the first label is one of the handful of second-level
// registries. Anything it misses, the browser refuses anyway; the difference is
// only whether the refusal is explained.
func publicSuffix(d string) bool {
	parts := strings.Split(d, ".")
	if len(parts) != 2 {
		return false
	}
	if len(parts[1]) != 2 { // not a country code
		return false
	}
	switch parts[0] {
	case "co", "com", "net", "org", "gov", "edu", "ac", "or", "ne", "me", "sch", "nhs", "ltd", "plc":
		return true
	}
	return false
}
