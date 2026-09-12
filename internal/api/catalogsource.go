package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/pkg/api"
)

// DefaultCatalogSource is the public catalog repository's main branch.
const DefaultCatalogSource = "https://github.com/isletdev/catalog/archive/refs/heads/main.tar.gz"

func (s *Server) catalogSource(ctx context.Context) catalog.Source {
	v, _, _ := s.store.Setting(ctx, "catalog.source")
	var src catalog.Source
	if v != "" {
		_ = json.Unmarshal([]byte(v), &src)
	}
	return src
}

func (s *Server) saveCatalogSource(ctx context.Context, src catalog.Source) {
	b, _ := json.Marshal(src)
	_ = s.store.SetSetting(ctx, "catalog.source", string(b))
}

// StartCatalogRefresh re-fetches a configured source once a day.
func (s *Server) StartCatalogRefresh(ctx context.Context) {
	s.catalog.LoadOverlay()
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			src := s.catalogSource(ctx)
			if src.URL == "" {
				continue
			}
			res, err := s.catalog.Refresh(ctx, src.URL)
			if err != nil {
				src.LastError = err.Error()
			} else {
				src = res
			}
			s.saveCatalogSource(ctx, src)
		}
	}()
}

// handleCatalogSource reads (GET), sets and refreshes (POST) or clears
// (DELETE) the runtime catalog source.
func (s *Server) handleCatalogSource(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		src := s.catalogSource(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"source": src, "default": DefaultCatalogSource})
		return
	case http.MethodDelete:
		_ = s.catalog.ClearOverlay()
		_ = s.store.SetSetting(r.Context(), "catalog.source", "")
		_ = s.store.Audit(r.Context(), u.Username, "catalog.source", "", "cleared")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct{ URL string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		req.URL = s.catalogSource(r.Context()).URL
	}
	if req.URL == "" {
		req.URL = DefaultCatalogSource
	}
	src, err := s.catalog.Refresh(r.Context(), req.URL)
	if err != nil {
		prev := s.catalogSource(r.Context())
		prev.URL, prev.LastError = req.URL, err.Error()
		s.saveCatalogSource(r.Context(), prev)
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "refresh", Message: err.Error()})
		return
	}
	s.saveCatalogSource(r.Context(), src)
	_ = s.store.Audit(r.Context(), u.Username, "catalog.source", req.URL, "refreshed")
	writeJSON(w, http.StatusOK, map[string]any{"source": src, "default": DefaultCatalogSource})
}
