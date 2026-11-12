package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// Site people are managed by admins only: who may reach a client's site is the
// same kind of decision as who may sign in to the panel.

func (s *Server) handleSiteUsers(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	actor := userFrom(r.Context()).Username
	if r.Method == http.MethodPost {
		var req struct {
			Username    string `json:"username"`
			Name        string `json:"name"`
			Password    string `json:"password"`
			RequireTOTP bool   `json:"requireTotp"`
		}
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		u, err := s.auth.CreateSiteUser(r.Context(), actor, req.Username, req.Name, req.Password, req.RequireTOTP)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, u)
		return
	}
	people, err := s.auth.SiteUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	routes, err := s.proxy.ProtectedRoutes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": people, "routes": routes})
}

func (s *Server) handleSiteUser(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	actor := userFrom(r.Context()).Username
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodPut:
		var p auth.SiteUserPatch
		if err := decode(r, &p); err != nil {
			s.badJSON(w, err)
			return
		}
		u, err := s.auth.UpdateSiteUser(r.Context(), actor, id, p)
		if err != nil {
			s.siteUserErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, u)
	case http.MethodDelete:
		name, err := s.auth.DeleteSiteUser(r.Context(), actor, id)
		if err != nil {
			s.siteUserErr(w, err)
			return
		}
		// Off every list that names them, so a person added later under the
		// same name does not inherit what this one could reach.
		if _, err := s.proxy.ForgetUser(r.Context(), actor, auth.SitePrefix+name); err != nil && s.log != nil {
			s.log.Warn("could not take a deleted site person off the protection lists", "person", name, "err", err)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleSiteUserAccess sets exactly which protected routes a person may reach.
func (s *Server) handleSiteUserAccess(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u, err := s.auth.SiteUser(r.Context(), r.PathValue("id"))
	if err != nil {
		s.siteUserErr(w, err)
		return
	}
	var req struct {
		Routes []string `json:"routes"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	want := map[proxy.RouteKey]bool{}
	for _, k := range req.Routes {
		want[k] = true
	}
	if err := s.proxy.SetAccess(r.Context(), userFrom(r.Context()).Username, auth.SitePrefix+u.Username, want); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "proxy", Message: "the access was saved but the proxy could not be updated: " + err.Error()})
		return
	}
	routes, _ := s.proxy.ProtectedRoutes(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
}

// handleSiteUserInvite makes a link for the person to choose their own
// password, on one of the protected domains: the page they will use afterwards
// is the one they set it on.
func (s *Server) handleSiteUserInvite(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Host string `json:"host"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	host := auth.NormHost(req.Host)
	if strings.HasPrefix(host, "*.") || s.proxy.ProtectedDomainFor(r.Context(), host) == nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "choose a protected domain for the link"})
		return
	}
	tok, exp, err := s.auth.SiteInvite(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"))
	if err != nil {
		s.siteUserErr(w, err)
		return
	}
	scheme := "https"
	if d := s.proxy.ProtectedDomainFor(r.Context(), host); d != nil && d.TLS == "none" {
		scheme = "http"
	}
	writeJSON(w, http.StatusOK, map[string]string{"link": scheme + "://" + host + "/_islet/invite?t=" + tok, "expiresAt": exp.Format("2006-01-02T15:04:05Z")})
}

func (s *Server) siteUserErr(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrNoSession) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such person"})
		return
	}
	writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
}

// handleLoginPage reads or sets how a protected domain's sign-in page looks.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodPut {
		if !s.adminOnly(w, r) {
			return
		}
		var p proxy.LoginPage
		if err := decode(r, &p); err != nil {
			s.badJSON(w, err)
			return
		}
		p.DomainID = id
		if err := s.proxy.SetLoginPage(r.Context(), userFrom(r.Context()).Username, p); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
	}
	p, err := s.proxy.LoginPage(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}
