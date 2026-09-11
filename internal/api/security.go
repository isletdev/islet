package api

import (
	"net/http"

	"github.com/isletdev/islet/internal/security"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "admins only"})
		return false
	}
	return true
}

func (s *Server) handleSecurityReport(w http.ResponseWriter, r *http.Request) {
	rep := s.security.Report(r.Context())
	fw := s.security.FirewallStatus(r.Context())
	ssh, keys, pending := s.security.SSH(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"report":      rep,
		"firewall":    fw,
		"ssh":         ssh,
		"sshHasKeys":  keys,
		"sshRollback": pending,
		"banned":      s.security.BannedIPs(r.Context()),
		"scans":       s.security.Scans(),
		"clientIp":    clientIP(r),
		"panelCidr":   s.security.PanelCIDR(r.Context()),
	})
}

func (s *Server) handleSecurityFix(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	out, err := s.security.Fix(r.Context(), u.Username, r.PathValue("id"), clientIP(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "fix_failed", Message: err.Error() + "\n" + out})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

func (s *Server) handleSecurityFixAll(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.security.FixAll(r.Context(), userFrom(r.Context()).Username, clientIP(r)))
}

func (s *Server) handleFirewallRule(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Port, Proto, From, Comment string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	var err error
	if r.Method == http.MethodDelete {
		err = s.security.DenyPort(r.Context(), u.Username, req.Port, req.Proto, req.From)
	} else {
		err = s.security.AllowPort(r.Context(), u.Username, req.Port, req.Proto, req.From, req.Comment)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "firewall", Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnban(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ IP string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if err := s.security.Unban(r.Context(), userFrom(r.Context()).Username, req.IP); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "unban", Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSSHApply(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var cfg security.SSHSettings
	if err := decode(r, &cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	msg, err := s.security.ApplySSH(r.Context(), userFrom(r.Context()).Username, cfg, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "ssh", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) handleSSHConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if !s.security.ConfirmSSH() {
		writeJSON(w, http.StatusConflict, api.Error{Error: "none", Message: "no SSH change is waiting for confirmation"})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "ssh.confirm", "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleScanImage(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Image string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	sc, err := s.security.ScanImage(r.Context(), userFrom(r.Context()).Username, req.Image)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "scan", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) handlePanelRestrict(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ CIDR string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if err := s.security.RestrictPanel(r.Context(), userFrom(r.Context()).Username, req.CIDR, clientIP(r)); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "firewall", Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLynis(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	out, score, err := s.security.Lynis(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "lynis", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"score": score, "output": out})
}

// handlePanic locks the server down to the caller's IP and rotates every
// session and token except the current one.
func (s *Server) handlePanic(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	out, err := s.security.Panic(r.Context(), u.Username, clientIP(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "panic", Message: err.Error() + "\n" + out})
		return
	}
	sess := sessionFrom(r.Context())
	_ = s.auth.RevokeOthers(r.Context(), sess.ID)
	writeJSON(w, http.StatusOK, map[string]string{"output": out, "message": "Inbound traffic is blocked except from " + clientIP(r) + ". Every other session and all API tokens are revoked. Undo from the Firewall section when you are done."})
}
