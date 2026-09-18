package api

import (
	"context"
	"net/http"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	userID := u.ID
	if u.Role == "admin" && r.URL.Query().Get("all") == "1" {
		userID = ""
	}
	list, err := s.auth.Tokens(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if tokenFrom(r.Context()) != nil {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "tokens cannot mint tokens; sign in with a browser"})
		return
	}
	var req struct {
		Name    string `json:"name"`
		Scopes  string `json:"scopes"`
		TTLDays int    `json:"ttlDays"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	// A token is its owner acting later, so it cannot carry more than the owner
	// has. Without this a viewer minted a token scoped to everything and used it
	// where the role check was weaker.
	if scopes, err := auth.CapScopes(req.Scopes, u.Role); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	} else {
		req.Scopes = scopes
	}
	plain, t, err := s.auth.CreateToken(r.Context(), u.ID, req.Name, req.Scopes, time.Duration(req.TTLDays)*24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "token.create", t.ID, t.Name+" ("+t.Scopes+")")
	writeJSON(w, http.StatusCreated, map[string]any{"token": plain, "info": t})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if err := s.auth.RevokeToken(r.Context(), u.ID, r.PathValue("id"), u.Role == "admin"); err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "token not found"})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "token.revoke", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func tokenFrom(ctx context.Context) *auth.Token {
	t, _ := ctx.Value(ctxToken).(*auth.Token)
	return t
}
