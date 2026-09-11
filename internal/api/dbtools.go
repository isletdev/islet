package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// handleDBAdminer makes sure Adminer is installed and can reach the
// instance, then returns a link that opens it on that database.
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
	driver := map[string]string{"postgres": "pgsql", "mysql": "server", "mongo": "mongo"}[inst.Engine]
	if driver == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "unsupported", Message: "Adminer does not speak " + inst.Engine + "; use the built-in console instead"})
		return
	}
	var stack *catalog.Installed
	if apps, err := s.catalog.InstalledApps(); err == nil {
		for i := range apps {
			if apps[i].Slug == "adminer" {
				stack = &apps[i]
				break
			}
		}
	}
	installed := false
	if stack == nil {
		req := catalog.InstallRequest{Slug: "adminer", Name: "adminer", Domain: proxy.PreviewHost(r.Context(), "adminer"), TLS: "letsencrypt"}
		rc, wait, err := s.catalog.Install(r.Context(), u.Username, req)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: err.Error()})
			return
		}
		_, _ = io.Copy(io.Discard, rc)
		if err := wait(); err != nil {
			writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: "Adminer install failed: " + err.Error()})
			return
		}
		_ = s.store.Audit(r.Context(), u.Username, "app.install", "adminer", "for database "+inst.Name)
		stack = &catalog.Installed{Slug: "adminer", Name: "adminer", Domain: req.Domain}
		installed = true
	}
	container := stack.Name + "-adminer-1"
	if inst.Network != "" {
		// Idempotent: docker refuses a second connect, which is fine.
		_, _ = s.runner.Run(r.Context(), u.Username, "docker", "network", "connect", inst.Network, container)
	}
	q := url.Values{}
	q.Set(driver, inst.Container)
	q.Set("username", inst.User)
	if inst.Database != "" {
		q.Set("db", inst.Database)
	}
	scheme := "https"
	if strings.HasSuffix(stack.Domain, ".sslip.io") && r.TLS == nil {
		scheme = "http"
	}
	link := scheme + "://" + stack.Domain + "/?" + q.Encode()
	if stack.Domain == "" {
		link = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "installed": installed, "password": inst.Password, "domain": stack.Domain})
}
