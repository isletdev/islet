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
	if err := s.store.SetSetting(r.Context(), "auth.cookie_domain", d); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError("internal", err.Error()))
		return
	}
	cookieDomain.Store(d)
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "auth.cookie_domain", d, "")
	writeJSON(w, http.StatusOK, map[string]string{"cookieDomain": d})
}
