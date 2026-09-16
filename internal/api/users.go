package api

import (
	"net/http"
	"strings"

	"github.com/isletdev/islet/internal/notify"
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
	// The name has to be read before the row goes, and it is what the
	// allow-lists are written in terms of.
	gone := ""
	if u, err := s.auth.UserByID(r.Context(), r.PathValue("id")); err == nil {
		gone = u.Username
	}
	if err := s.auth.DeleteUser(r.Context(), r.PathValue("id"), me.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), me.Username, "user.delete", r.PathValue("id"), "")
	// A deleted account must not stay on an allow-list. Nothing would let it
	// in — there is nobody to sign in as — but usernames are reusable, and a
	// new account with an old name would inherit whatever the old one could
	// reach. The deletion has already happened, so a failure here is logged
	// rather than returned: the account is gone either way.
	if gone != "" {
		res, err := s.proxy.ForgetUser(r.Context(), me.Username, gone)
		if err != nil {
			s.log.Warn("could not take a deleted user off the protection lists", "user", gone, "err", err)
		} else if res.Rules > 0 {
			s.log.Info("removed a deleted user from protection lists", "user", gone, "rules", res.Rules)
		}
		// A list that named nobody else now names nobody at all, which means
		// any signed-in user. The route still asks for a login, but it stopped
		// asking for a particular person, and that is not something to change
		// on somebody's behalf without saying so.
		if len(res.Widened) > 0 && s.notify != nil {
			s.notify.Emit(r.Context(), notify.Event{
				Category: "security", Severity: notify.Warning,
				Title:   "Protected routes lost their last named user",
				Message: "Deleting " + gone + " emptied the allow-list on " + strings.Join(res.Widened, ", ") + ". Those routes still require an Islet login, but now any signed-in user passes. Set who may reach them under Domains, Protection.",
				Link:    "/domains",
			})
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
