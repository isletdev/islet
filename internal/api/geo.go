package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/pkg/api"
)

// geoEnabled reports whether new-address login alerts may look up the
// address's country. Off by default: it sends the visitor's address to a
// third party (ipapi.co).
func (s *Server) geoEnabled(ctx context.Context) bool {
	v, _, _ := s.store.Setting(ctx, "auth.geo")
	return v == "on"
}

// lookupGeo returns "City, Country" for a public address, or "".
func lookupGeo(ctx context.Context, ip string) string {
	p := net.ParseIP(ip)
	if p == nil || p.IsPrivate() || p.IsLoopback() || p.IsLinkLocalUnicast() {
		return ""
	}
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, http.MethodGet, "https://ipapi.co/"+ip+"/json/", nil)
	req.Header.Set("User-Agent", "islet")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		City    string `json:"city"`
		Country string `json:"country_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&out); err != nil {
		return ""
	}
	parts := []string{}
	if out.City != "" {
		parts = append(parts, out.City)
	}
	if out.Country != "" {
		parts = append(parts, out.Country)
	}
	return strings.Join(parts, ", ")
}

func (s *Server) handleGeoSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.geoEnabled(r.Context())})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	v := "off"
	if req.Enabled {
		v = "on"
	}
	if err := s.store.SetSetting(r.Context(), "auth.geo", v); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}
