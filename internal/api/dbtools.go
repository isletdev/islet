package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// adminerStack is the Compose project Islet installs for the browser client.
// One instance serves every database: it is attached to each instance's
// network as it is used.
const adminerStack = "adminer"

const adminerContainer = adminerStack + "-adminer-1"

// adminerSetup is returned when Adminer has to be installed first, so the
// panel can ask where it should live instead of picking for the user.
type adminerSetup struct {
	Error         string `json:"error"`
	Message       string `json:"message"`
	SetupRequired bool   `json:"setupRequired"`
	SuggestedHost string `json:"suggestedHost"`
	PanelHost     string `json:"panelHost,omitempty"`
	CookieDomain  string `json:"cookieDomain,omitempty"`
}

// adminerInstalled returns the installed stack, or nil.
func (s *Server) adminerInstalled() *catalog.Installed {
	apps, err := s.catalog.InstalledApps()
	if err != nil {
		return nil
	}
	for i := range apps {
		if apps[i].Slug == "adminer" {
			return &apps[i]
		}
	}
	return nil
}

// panelHost is the domain that serves the panel, when one is routed.
func (s *Server) panelHost(ctx context.Context) string {
	doms, err := s.proxy.Domains(ctx)
	if err != nil {
		return ""
	}
	for _, d := range doms {
		if d.TargetType == "panel" && d.Enabled {
			return d.Host
		}
	}
	return ""
}

// handleDBAdminer opens Adminer on a database, installing it on first use.
// The request may carry host, tls and protect for that first install.
func (s *Server) handleDBAdminer(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	inst, err := s.db.Get(r.Context(), u.Username, r.PathValue("name"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	driver := map[string]string{"postgres": "pgsql", "mysql": "server", "mariadb": "server", "mongo": "mongo"}[inst.Engine]
	if driver == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "unsupported", Message: "Adminer does not speak " + inst.Engine + "; use the built-in console instead"})
		return
	}
	var req struct {
		Host    string `json:"host"`
		TLS     string `json:"tls"`
		Protect *bool  `json:"protect"`
	}
	_ = decode(r, &req)
	req.Host = strings.ToLower(strings.TrimSpace(req.Host))

	stack := s.adminerInstalled()
	installed := false
	if stack == nil {
		if req.Host == "" {
			cookie, _, _ := s.store.Setting(r.Context(), "auth.cookie_domain")
			writeJSON(w, http.StatusConflict, adminerSetup{
				Error:         "setup_required",
				Message:       "Adminer is not installed yet. Choose the host it should answer on.",
				SetupRequired: true,
				SuggestedHost: proxy.PreviewHost(r.Context(), "adminer"),
				PanelHost:     s.panelHost(r.Context()),
				CookieDomain:  cookie,
			})
			return
		}
		if strings.ContainsAny(req.Host, " /:") {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "that is not a host name"})
			return
		}
		tls := req.TLS
		if tls != "letsencrypt" && tls != "self" && tls != "none" {
			tls = "letsencrypt"
		}
		// Install without a domain, then route it here so the protect flag and
		// the certificate choice are applied in one place.
		rc, wait, err := s.catalog.Install(r.Context(), u.Username, catalog.InstallRequest{Slug: "adminer", Name: adminerStack, Fields: map[string]string{}})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: err.Error()})
			return
		}
		_, _ = io.Copy(io.Discard, rc)
		if err := wait(); err != nil {
			writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: "Adminer install failed: " + err.Error()})
			return
		}
		protect := true
		if req.Protect != nil {
			protect = *req.Protect
		}
		dom := &proxy.Domain{Host: req.Host, TargetType: "container", Target: adminerContainer, Port: 8080, TLS: tls, Protect: protect, Enabled: true}
		if _, err := s.proxy.Save(r.Context(), u.Username, dom); err != nil {
			writeJSON(w, http.StatusBadGateway, api.Error{Error: "route", Message: "Adminer is running but the domain could not be routed: " + err.Error()})
			return
		}
		_ = s.store.Audit(r.Context(), u.Username, "app.install", "adminer", req.Host)
		stack = &catalog.Installed{Slug: "adminer", Name: adminerStack, Domain: req.Host}
		installed = true
	}

	// Adminer reaches a database over that instance's own network.
	if inst.Network != "" {
		_, _ = s.runner.Run(r.Context(), u.Username, "docker", "network", "connect", inst.Network, adminerContainer)
	}

	host := stack.Domain
	if host == "" {
		for _, d := range mustDomains(r.Context(), s) {
			if d.TargetType == "container" && d.Target == adminerContainer && d.Enabled {
				host = d.Host
				break
			}
		}
	}
	scheme := "https"
	for _, d := range mustDomains(r.Context(), s) {
		if d.Host == host && d.TLS == "none" {
			scheme = "http"
		}
	}
	q := url.Values{}
	q.Set(driver, inst.Container)
	q.Set("username", inst.User)
	if inst.Database != "" {
		q.Set("db", inst.Database)
	}
	link := ""
	if host != "" {
		link = scheme + "://" + host + "/?" + q.Encode()
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "installed": installed, "password": inst.Password, "domain": host})
}

func mustDomains(ctx context.Context, s *Server) []proxy.Domain {
	doms, err := s.proxy.Domains(ctx)
	if err != nil {
		return nil
	}
	return doms
}
