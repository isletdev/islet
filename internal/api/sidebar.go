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
	// Auto marks a link the panel worked out for itself, from an app installed
	// from the catalog with a domain. It is not part of the list anyone edits,
	// and saving the list must not turn one into a link somebody typed.
	Auto bool `json:"auto,omitempty"`
}

// sidebarLinks is what the sidebar shows: the links somebody added by hand,
// and every app installed from the catalog that has a domain.
//
// An app with a domain is a thing you open. Making the person add a link to
// something the panel installed, and knows the address of, is asking them to
// tell us what we already told them.
func (s *Server) sidebarLinks(r *http.Request) []SidebarLink {
	out := s.manualLinks(r)
	seen := map[string]bool{}
	for _, l := range out {
		seen[strings.ToLower(l.URL)] = true
	}
	if s.catalog != nil {
		apps, err := s.catalog.InstalledApps()
		if err == nil {
			for _, a := range apps {
				if a.Domain == "" {
					continue
				}
				url := "https://" + a.Domain
				if seen[strings.ToLower(url)] {
					continue // a hand-written link wins; it may have a path
				}
				seen[strings.ToLower(url)] = true
				out = append(out, SidebarLink{Label: a.Name, URL: url, Auto: true})
			}
		}
	}
	return out
}

// manualLinks is only what was typed, which is what the editor edits and what
// a save writes back.
func (s *Server) manualLinks(r *http.Request) []SidebarLink {
	v, _, _ := s.store.Setting(r.Context(), "sidebar.links")
	out := []SidebarLink{}
	if v != "" {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (s *Server) handleSidebar(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// The editor asks for only the links it may change.
		if r.URL.Query().Get("manual") == "1" {
			writeJSON(w, http.StatusOK, s.manualLinks(r))
			return
		}
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
		if l.Auto {
			continue // the panel works these out; they are not stored
		}
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
