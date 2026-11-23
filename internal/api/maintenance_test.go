package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/proxy"
)

// A browser gets a page; an API client — asked for JSON, sent JSON, or reached
// a path under /api before Traefik replaced it — gets the same message as
// JSON. Both are 503 with Retry-After, for any method.
func TestMaintenanceAnswersBrowsersAndAPIs(t *testing.T) {
	s, _ := siteGateServer(t)
	if _, err := s.proxy.SetMaintenance(context.Background(), "admin", proxy.Maintenance{On: true, Message: "Moving to a bigger server."}); err != nil {
		t.Fatal(err)
	}
	ask := func(method string, h map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/_islet/maintenance", strings.NewReader(`{"a":1}`))
		for k, v := range h {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.handleMaintenancePage(w, r)
		return w
	}
	w := ask("GET", map[string]string{"Accept": "text/html,application/xhtml+xml,*/*;q=0.8", "X-Replaced-Path": "/shop"})
	if w.Code != http.StatusServiceUnavailable || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "Moving to a bigger server.") || w.Header().Get("Retry-After") == "" {
		t.Fatalf("browser got %d %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	for _, h := range []map[string]string{
		{"Accept": "application/json"},
		{"Content-Type": "application/json"},
		{"X-Replaced-Path": "/api/v2/orders"},
	} {
		for _, m := range []string{"GET", "POST", "DELETE"} {
			w := ask(m, h)
			if w.Code != http.StatusServiceUnavailable || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), `"error":"maintenance"`) {
				t.Errorf("%s with %v got %d %q: %s", m, h, w.Code, w.Header().Get("Content-Type"), w.Body.String())
			}
		}
	}
}
