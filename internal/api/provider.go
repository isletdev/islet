package api

import (
	"errors"
	"net/http"

	"github.com/isletdev/islet/internal/provider"
	"github.com/isletdev/islet/pkg/api"
)

// handleProvider reads or sets the hosting provider token.
func (s *Server) handleProvider(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if s.provider == nil {
		writeJSON(w, http.StatusOK, provider.State{})
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.provider.State(r.Context()))
		return
	}
	var req struct{ Kind, Token string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if err := s.provider.SetToken(r.Context(), userFrom(r.Context()).Username, req.Kind, req.Token); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.provider.State(r.Context()))
}

// handleProviderSnapshot requests a snapshot now.
func (s *Server) handleProviderSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if s.provider == nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: provider.ErrNotConfigured.Error()})
		return
	}
	desc, err := s.provider.Snapshot(r.Context(), userFrom(r.Context()).Username, "manual")
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, provider.ErrNotConfigured) {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, api.Error{Error: "snapshot", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"snapshot": desc})
}
