// Package api is the only package that knows about HTTP. It wires routes,
// middleware, and JSON encoding around the feature packages.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

// Server holds the dependencies handlers need.
type Server struct {
	store   *store.Store
	ui      http.Handler
	log     *slog.Logger
	started time.Time
}

// New builds the HTTP handler for the daemon.
func New(st *store.Store, ui http.Handler, log *slog.Logger) http.Handler {
	s := &Server{store: st, ui: ui, log: log, started: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/", s.notFound)
	mux.Handle("/", ui)
	return s.recover(s.logRequests(s.securityHeaders(mux)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Health{
		Status:        "ok",
		Version:       version.Version,
		Commit:        version.Commit,
		ServerID:      s.store.ServerID,
		Hostname:      s.store.Hostname,
		UptimeSeconds: int64(time.Since(s.started).Seconds()),
		Time:          time.Now().UTC(),
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such API route"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- middleware ----

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "path", r.URL.Path, "err", rec, "stack", string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
