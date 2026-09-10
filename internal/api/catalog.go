package api

import (
	"net/http"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := s.catalog.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCatalogApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.catalog.Get(r.PathValue("slug"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot install apps"})
		return
	}
	var req catalog.InstallRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	req.Slug = r.PathValue("slug")
	rc, wait, err := s.catalog.Install(r.Context(), u.Username, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.install", req.Slug, "name="+req.Name+" domain="+req.Domain)
	streamLines(w, r, rc, wait)
}

func (s *Server) handleInstalledApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.catalog.InstalledApps()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if userFrom(r.Context()).Role != "admin" {
		for i := range list {
			list[i].Values = nil
		}
	}
	writeJSON(w, http.StatusOK, list)
}
