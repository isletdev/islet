package api

import (
	"net/http"
	"strings"

	"github.com/isletdev/islet/internal/vault"
	"github.com/isletdev/islet/pkg/api"
)

// handleVault lists secrets and stores them. A listing carries names,
// descriptions and when each was last used — never a value.
func (s *Server) handleVault(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "unavailable", Message: "the vault is not available"})
		return
	}
	if r.Method == http.MethodGet {
		list, err := s.vault.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, list)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Value       string `json:"value"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	if err := s.vault.Set(r.Context(), req.Name, req.Value, req.Description); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	// The name is recorded, the value is not. An audit log that carried the
	// values would be a second copy of the vault, in the clear.
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "vault.set", strings.TrimSpace(req.Name), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleVaultPut writes one secret by name, which is the shape every other
// collection uses. It shares the body with POST /vault so a client can send
// either; the name in the path wins, because that is what a PUT means.
func (s *Server) handleVaultPut(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "unavailable", Message: "the vault is not available"})
		return
	}
	var req struct {
		Value       string `json:"value"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := s.vault.Set(r.Context(), name, req.Value, req.Description); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "vault.set", name, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleVaultDelete(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "unavailable", Message: "the vault is not available"})
		return
	}
	name := r.PathValue("name")
	if err := s.vault.Delete(r.Context(), name); err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "vault.delete", name, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleVaultReveal returns one value in the clear.
//
// Admins only, and written down every time. The point of a vault is that a
// secret goes in and is used without coming back out; this exists because
// sometimes a person genuinely needs to read one — to paste it into something
// Islet does not manage — and the honest answer to that is a door with a light
// on it rather than no door and a habit of keeping copies elsewhere.
func (s *Server) handleVaultReveal(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "unavailable", Message: "the vault is not available"})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	name := r.PathValue("name")
	v, err := s.vault.Value(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "vault.reveal", name, "shown in the panel")
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "value": v})
}

// expandSecrets resolves @vault:NAME references. Callers that put values into a
// process — an app's environment, a cron job's command — run their input
// through this first.
func (s *Server) expandSecrets(ctx contextCtx, in string) string {
	if s.vault == nil || !strings.Contains(in, "@vault:") {
		return in
	}
	out, missing, err := s.vault.Expand(ctx, in)
	if err != nil {
		return in
	}
	if len(missing) > 0 {
		s.log.Warn("vault: reference to a secret that does not exist", "names", strings.Join(missing, ","))
	}
	return out
}

var _ = vault.Refs // the reference syntax is defined once, in the vault package
