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

// Shared environment groups: named KEY=VALUE sets that apps pull in with a
// "@name" line. Stored encrypted as one JSON document.

var groupNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func (s *Server) envGroups(ctx context.Context) map[string]string {
	out := map[string]string{}
	v, _, _ := s.store.Setting(ctx, "deploy.env_groups")
	if v == "" {
		return out
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return out
	}
	p, err := s.keys.Decrypt(b)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(p, &out)
	return out
}

func (s *Server) saveEnvGroups(ctx context.Context, g map[string]string) error {
	b, _ := json.Marshal(g)
	enc, err := s.keys.Encrypt(b)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, "deploy.env_groups", base64.StdEncoding.EncodeToString(enc))
}

// EnvGroupLines is the resolver handed to the deploy service.
func (s *Server) EnvGroupLines(ctx context.Context, name string) ([]string, bool) {
	v, ok := s.envGroups(ctx)[name]
	if !ok {
		return nil, false
	}
	var lines []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return lines, true
}

func (s *Server) handleEnvGroups(w http.ResponseWriter, r *http.Request) {
	g := s.envGroups(r.Context())
	type row struct {
		Name string   `json:"name"`
		Keys []string `json:"keys"`
		Env  string   `json:"env,omitempty"`
	}
	out := []row{}
	admin := userFrom(r.Context()).Role == "admin"
	for name, v := range g {
		var keys []string
		for _, l := range strings.Split(v, "\n") {
			if k, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
				keys = append(keys, k)
			}
		}
		rw := row{Name: name, Keys: keys}
		if admin {
			rw.Env = v
		}
		out = append(out, rw)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEnvGroupSave(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Name, Env string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if !groupNameRe.MatchString(req.Name) {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "name must be lowercase letters, digits or dashes"})
		return
	}
	for _, l := range strings.Split(req.Env, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if k, _, ok := strings.Cut(l, "="); !ok || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "line " + l + " is not KEY=VALUE"})
			return
		}
	}
	g := s.envGroups(r.Context())
	g[req.Name] = strings.TrimSpace(req.Env)
	if err := s.saveEnvGroups(r.Context(), g); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "envgroup.save", req.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEnvGroupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	g := s.envGroups(r.Context())
	delete(g, r.PathValue("name"))
	if err := s.saveEnvGroups(r.Context(), g); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "envgroup.delete", r.PathValue("name"), "")
	w.WriteHeader(http.StatusNoContent)
}
