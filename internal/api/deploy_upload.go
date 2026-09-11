package api

import (
	"fmt"
	"net/http"

	"github.com/isletdev/islet/pkg/api"
)

// handleAppUpload stores a dropped folder or zip as the app's source.
func (s *Server) handleAppUpload(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins upload app sources"})
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "multipart body expected"})
		return
	}
	meta, det, err := s.deploy.Upload(r.Context(), u.Username, r.PathValue("id"), mr)
	if err != nil {
		s.deployErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.upload", r.PathValue("id"), fmt.Sprintf("%s (%d files)", meta.Name, meta.Files))
	writeJSON(w, http.StatusOK, map[string]any{"upload": meta, "detection": det})
}
