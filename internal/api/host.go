package api

import (
	"net/http"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/pkg/api"
)

// handleHostUser creates a sudo user with an optional SSH key.
func (s *Server) handleHostUser(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Name, PublicKey string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	out, err := s.security.CreateSudoUser(r.Context(), userFrom(r.Context()).Username, req.Name, req.PublicKey)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "failed", Message: cmdrun.Redact(err.Error() + "\n" + out)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

// handleHostSSHKey appends a public key to a user's authorized_keys.
func (s *Server) handleHostSSHKey(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ User, PublicKey string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	if req.User == "" {
		req.User = "root"
	}
	out, err := s.security.AddSSHKey(r.Context(), userFrom(r.Context()).Username, req.User, req.PublicKey)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "failed", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

// handleHostTimezone reads or sets the system timezone.
func (s *Server) handleHostTimezone(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]string{"timezone": s.security.Timezone(r.Context())})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Timezone string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	out, err := s.security.SetTimezone(r.Context(), userFrom(r.Context()).Username, req.Timezone)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "failed", Message: cmdrun.Redact(err.Error() + "\n" + out)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"timezone": req.Timezone})
}

// handleHostAudit returns the last audit (GET) or runs one (POST).
func (s *Server) handleHostAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.security.LastHostAudit())
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Rkhunter bool }
	_ = decode(r, &req)
	a, err := s.security.RunHostAudit(r.Context(), userFrom(r.Context()).Username, req.Rkhunter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "failed", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleHostBaseline records the current /etc state as the reference.
func (s *Server) handleHostBaseline(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	n, err := s.security.SetBaseline(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "failed", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"files": n})
}
