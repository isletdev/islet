package api

import (
	"errors"
	"net/http"

	"github.com/isletdev/islet/internal/work"
	"github.com/isletdev/islet/pkg/api"
)

// What the server is working on.
//
// There is no Work page and there should not be one: a queue is not a thing
// people want to look at, it is a thing they want to not have to look at. These
// routes exist so the page that asked for the work can show it — Media draws the
// transcodes of the object in front of you — and so a failure has somewhere to
// be read after the notification has been dismissed.

func (s *Server) workOK(w http.ResponseWriter) bool {
	if s.work == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Error{Error: "unavailable", Message: "the work queue is not running on this server"})
		return false
	}
	return true
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) || !s.adminOnly(w, r) {
		return
	}
	list, err := s.work.List(r.Context(), r.URL.Query().Get("subject"), atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		s.failed(w, "work", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTask1(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) || !s.adminOnly(w, r) {
		return
	}
	t, err := s.work.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such task"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) || !s.adminOnly(w, r) {
		return
	}
	err := s.work.Cancel(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"))
	if errors.Is(err, work.ErrNoTask) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such task"})
		return
	}
	if err != nil {
		s.failed(w, "work", err)
		return
	}
	// What it is now, so a caller can tell a cancel that stopped something from
	// one that arrived after it had already finished. Both are 200: the state
	// asked for holds either way.
	t, err := s.work.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": t.State})
}
