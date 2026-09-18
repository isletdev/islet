package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/isletdev/islet/pkg/api"
)

type contextCtx = context.Context

// Registry logins let pulls and deploys use private images. Credentials
// are stored encrypted in settings and applied with `docker login`, so the
// Docker daemon keeps them in its own config for every later pull.

type registry struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

var registryHostRe = regexp.MustCompile(`^[a-z0-9.-]+(:\d+)?$`)

func (s *Server) registries(ctx contextCtx) []registry {
	v, _, _ := s.store.Setting(ctx, "docker.registries")
	if v == "" {
		return []registry{}
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return []registry{}
	}
	p, err := s.keys.Decrypt(b)
	if err != nil {
		return []registry{}
	}
	var out []registry
	_ = json.Unmarshal(p, &out)
	return out
}

func (s *Server) saveRegistries(ctx contextCtx, list []registry) error {
	b, _ := json.Marshal(list)
	enc, err := s.keys.Encrypt(b)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, "docker.registries", base64.StdEncoding.EncodeToString(enc))
}

func (s *Server) handleRegistries(w http.ResponseWriter, r *http.Request) {
	list := s.registries(r.Context())
	for i := range list {
		list[i].Password = ""
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRegistryLogin(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req registry
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	req.Host = strings.ToLower(strings.TrimSpace(req.Host))
	if req.Host == "" || req.Host == "docker.io" || req.Host == "hub.docker.com" {
		req.Host = "docker.io"
	} else if !registryHostRe.MatchString(req.Host) {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "host must be a registry host like ghcr.io or registry.example.com:5000"})
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "username and password (or token) are required"})
		return
	}
	u := userFrom(r.Context())
	host := req.Host
	if host == "docker.io" {
		host = ""
	}
	args := []string{"login", "-u", req.Username, "--password-stdin"}
	if host != "" {
		args = append(args, host)
	}
	if _, err := s.runner.RunInput(r.Context(), u.Username, []byte(req.Password), "docker", args...); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "login", Message: "docker login failed: " + strings.TrimSpace(err.Error())})
		return
	}
	list := s.registries(r.Context())
	kept := list[:0]
	for _, x := range list {
		if x.Host != req.Host {
			kept = append(kept, x)
		}
	}
	kept = append(kept, req)
	if err := s.saveRegistries(r.Context(), kept); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "registry.login", req.Host, req.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRegistryLogout(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	host := strings.ToLower(r.PathValue("host"))
	u := userFrom(r.Context())
	args := []string{"logout"}
	if host != "docker.io" {
		args = append(args, host)
	}
	_, _ = s.runner.Run(r.Context(), u.Username, "docker", args...)
	list := s.registries(r.Context())
	kept := list[:0]
	for _, x := range list {
		if x.Host != host {
			kept = append(kept, x)
		}
	}
	_ = s.saveRegistries(r.Context(), kept)
	_ = s.store.Audit(r.Context(), u.Username, "registry.logout", host, "")
	w.WriteHeader(http.StatusNoContent)
}
