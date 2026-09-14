package api

import (
	"net/http"

	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	list, err := s.auth.Users(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	out := make([]api.User, 0, len(list))
	for _, u := range list {
		out = append(out, api.User{ID: u.ID, Username: u.Username, Role: u.Role, Projects: u.Projects, TOTPEnabled: u.TOTPEnabled, IsService: u.IsService, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Username, Password, Role string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u, err := s.auth.CreateUser(r.Context(), req.Username, req.Password, req.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "user.create", u.ID, u.Username+" ("+u.Role+")")
	writeJSON(w, http.StatusCreated, api.User{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt})
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Role, Password string
		Projects       *string
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	me := userFrom(r.Context())
	id := r.PathValue("id")
	if req.Role != "" || req.Password != "" {
		if err := s.auth.UpdateUser(r.Context(), id, req.Role, req.Password, me.ID); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
	}
	if req.Projects != nil {
		if err := s.auth.SetProjects(r.Context(), id, *req.Projects); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		_ = s.store.Audit(r.Context(), me.Username, "user.projects", id, *req.Projects)
	}
	_ = s.store.Audit(r.Context(), me.Username, "user.update", id, "role="+req.Role+" password="+map[bool]string{true: "changed", false: "kept"}[req.Password != ""])
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	me := userFrom(r.Context())
	if err := s.auth.DeleteUser(r.Context(), r.PathValue("id"), me.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), me.Username, "user.delete", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}
