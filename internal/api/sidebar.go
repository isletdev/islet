package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/isletdev/islet/pkg/api"
)

// SidebarLink embeds an app's UI in the panel behind the Islet login.
type SidebarLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func (s *Server) sidebarLinks(r *http.Request) []SidebarLink {
	v, _, _ := s.store.Setting(r.Context(), "sidebar.links")
	out := []SidebarLink{}
	if v != "" {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (s *Server) handleSidebar(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.sidebarLinks(r))
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var links []SidebarLink
	if err := decode(r, &links); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	clean := []SidebarLink{}
	for _, l := range links {
		l.Label = strings.TrimSpace(l.Label)
		l.URL = strings.TrimSpace(l.URL)
		if l.Label == "" || l.URL == "" {
			continue
		}
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "link " + l.Label + " must be an http(s) URL"})
			return
		}
		if len(l.Label) > 30 {
			l.Label = l.Label[:30]
		}
		clean = append(clean, l)
		if len(clean) == 12 {
			break
		}
	}
	b, _ := json.Marshal(clean)
	if err := s.store.SetSetting(r.Context(), "sidebar.links", string(b)); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "sidebar.links", "", string(b))
	writeJSON(w, http.StatusOK, clean)
}
