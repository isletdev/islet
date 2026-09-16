package api

import (
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

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
// A valid, MFA-complete panel session passes with X-Islet-User; anything
// else is sent to the panel login with a return address.
func (s *Server) handleForwardAuth(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u != nil {
		w.Header().Set("X-Islet-User", u.Username)
		w.Header().Set("X-Islet-Role", u.Role)
		w.WriteHeader(http.StatusOK)
		return
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "https"
	}
	next := proto + "://" + r.Header.Get("X-Forwarded-Host") + r.Header.Get("X-Forwarded-Uri")
	panel := ""
	if doms, err := s.proxy.Domains(r.Context()); err == nil {
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
		if ip := proxy.PublicIP(r.Context()); ip != "" {
			panel = "https://" + ip + ":9443"
		} else {
			http.Error(w, "sign in to the Islet panel first", http.StatusUnauthorized)
			return
		}
	}
	http.Redirect(w, r, panel+"/login?next="+url.QueryEscape(next), http.StatusFound)
}

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

	// Re-issue this session at the new scope, rather than telling somebody to
	// sign out and back in. Widening the domain is nearly always done *because*
	// a protected site just turned them away, and the old cookie is host-only —
	// so without this the next attempt is refused again, by the same session
	// they are sitting in, and the setting looks broken.
	//
	// The value is unchanged: it is the same session, offered to the browser
	// with a wider Domain. The stale cookie at the previous scope is cleared,
	// or two would linger with different lifetimes and the older one would win
	// on some paths.
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if old != "" && old != d {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
				Secure: isSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1, Domain: old,
			})
		}
		if sess := sessionFrom(r.Context()); sess != nil {
			setSessionCookie(w, r, c.Value, sess.ExpiresAt)
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
