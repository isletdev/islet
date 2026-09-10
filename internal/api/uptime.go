package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/isletdev/islet/internal/uptime"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) uptimeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, uptime.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
}

func (s *Server) handleChecks(w http.ResponseWriter, r *http.Request) {
	list, err := s.uptime.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCheckSave(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot edit checks"})
		return
	}
	c := uptime.Check{Enabled: true}
	if err := decode(r, &c); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	c.ID = r.PathValue("id")
	saved, err := s.uptime.Save(r.Context(), &c)
	if err != nil {
		s.uptimeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "check.save", saved.ID, saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleCheckDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot edit checks"})
		return
	}
	if err := s.uptime.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.uptimeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "check.delete", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCheckResults(w http.ResponseWriter, r *http.Request) {
	limit := 120
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 5000 {
		limit = v
	}
	list, err := s.uptime.Results(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCheckProbe(w http.ResponseWriter, r *http.Request) {
	res, err := s.uptime.Probe(r.Context(), r.PathValue("id"))
	if err != nil {
		s.uptimeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
