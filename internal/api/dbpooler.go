package api

import (
	"net/http"
	"strconv"
)

// handleDBPooler turns PgBouncer on or off for a Postgres instance.
func (s *Server) handleDBPooler(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	u := userFrom(r.Context())
	rc, wait, err := s.db.SetPooler(r.Context(), u.Username, inst, req.Enabled)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.pooler", inst.Name, strconv.FormatBool(req.Enabled))
	streamLines(w, r, rc, wait)
}
