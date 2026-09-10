package api

import (
	"net/http"
	"strconv"

	"github.com/isletdev/islet/pkg/api"
)

// handleAudit lists audit entries newest first. Pagination: pass before=<id>
// from the last row to get older ones.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	before := int64(0)
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil && v > 0 {
		before = v
	}
	q := `SELECT id, actor, action, target, detail, created_at FROM audit_log WHERE server_id = ?`
	args := []any{s.store.ServerID}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.store.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	defer rows.Close()
	out := []api.AuditEntry{}
	for rows.Next() {
		var e api.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}
