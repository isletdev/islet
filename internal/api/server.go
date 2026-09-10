// Package api is the only package that knows about HTTP. It wires routes,
// middleware, and JSON encoding around the feature packages.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

// Server holds the dependencies handlers need.
type Server struct {
	store   *store.Store
	auth    *auth.Service
	ui      http.Handler
	log     *slog.Logger
	started time.Time
}

// New builds the HTTP handler for the daemon.
func New(st *store.Store, as *auth.Service, ui http.Handler, log *slog.Logger) http.Handler {
	s := &Server{store: st, auth: as, ui: ui, log: log, started: time.Now()}
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", requireJSON(s.handleSetup))
	mux.HandleFunc("POST /api/v1/auth/login", requireJSON(s.handleLogin))
	mux.HandleFunc("POST /api/v1/auth/mfa", requireJSON(s.handleMFAVerify))
	mux.HandleFunc("POST /api/v1/auth/logout", requireJSON(s.handleLogout))

	// Signed in
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/v1/auth/password", requireJSON(s.requireAuth(s.handlePasswordChange)))
	mux.HandleFunc("POST /api/v1/auth/totp/setup", requireJSON(s.requireAuth(s.handleTOTPSetup)))
	mux.HandleFunc("POST /api/v1/auth/totp/enable", requireJSON(s.requireAuth(s.handleTOTPEnable)))
	mux.HandleFunc("POST /api/v1/auth/totp/disable", requireJSON(s.requireAuth(s.handleTOTPDisable)))
	mux.HandleFunc("GET /api/v1/auth/sessions", s.requireAuth(s.handleSessions))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{id}", requireJSON(s.requireAuth(s.handleSessionRevoke)))

	mux.HandleFunc("/api/", s.notFound)
	mux.Handle("/", ui)
	return s.recover(s.logRequests(s.securityHeaders(s.withSession(mux))))
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
	w.Header().Set("Cache-Control", "no-store")
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
