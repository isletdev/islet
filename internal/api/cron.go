package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) cronErr(w http.ResponseWriter, err error) {
	if errors.Is(err, cron.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	list, err := s.cron.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if userFrom(r.Context()).Role == "viewer" {
		for i := range list {
			list[i].Command, list[i].Script = "", ""
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.cron.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.cronErr(w, err)
		return
	}
	if userFrom(r.Context()).Role == "viewer" {
		j.Command, j.Script = "", ""
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleJobSave(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins edit jobs"})
		return
	}
	j := cron.Job{Enabled: true} // absent field means enabled
	if err := decode(r, &j); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	j.ID = r.PathValue("id")
	saved, err := s.cron.Save(r.Context(), u.Username, &j)
	if err != nil {
		s.cronErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "job.save", saved.ID, saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins edit jobs"})
		return
	}
	if err := s.cron.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.cronErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "job.delete", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

// handleJobRun starts the job and streams its output as SSE until it ends.
func (s *Server) handleJobRun(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot run jobs"})
		return
	}
	id := r.PathValue("id")
	if _, err := s.cron.Get(r.Context(), id); err != nil {
		s.cronErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "job.run", id, "")
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		// The run outlives a closed browser tab: a manual backup must not die
		// because the user navigated away.
		err := s.cron.Run(context.Background(), id, "manual", pw)
		pw.Close()
		done <- err
	}()
	// If the client went away, closing the reader unblocks the job's writes.
	streamLines(w, r, pr, func() error { pr.Close(); return <-done })
}

func (s *Server) handleJobKill(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot stop jobs"})
		return
	}
	if !s.cron.Kill(r.PathValue("id")) {
		writeJSON(w, http.StatusConflict, api.Error{Error: "not_running", Message: "the job is not running"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleJobRuns(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	list, err := s.cron.Runs(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleJobRunDetail(w http.ResponseWriter, r *http.Request) {
	rid, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, err := s.cron.RunDetail(r.Context(), r.PathValue("id"), rid)
	if err != nil {
		s.cronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleJobVersions(w http.ResponseWriter, r *http.Request) {
	list, err := s.cron.Versions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleJobVersion(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot read scripts"})
		return
	}
	vid, _ := strconv.ParseInt(r.PathValue("version"), 10, 64)
	v, err := s.cron.Version(r.Context(), r.PathValue("id"), vid)
	if err != nil {
		s.cronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleJobExport(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot export jobs"})
		return
	}
	j, err := s.cron.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.cronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"crontab": s.cron.CrontabLine(j), "scriptPath": s.cron.ScriptPath(j.ID)})
}

func (s *Server) handleCronPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schedule string `json:"schedule"`
		Timezone string `json:"timezone"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	desc, next, err := cron.Preview(req.Schedule, req.Timezone, 5)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	out := struct {
		Described string   `json:"described"`
		Next      []string `json:"next"`
	}{Described: desc, Next: []string{}}
	for _, t := range next {
		out.Next = append(out.Next, t.Format(time.RFC3339))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCronTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, cron.Templates)
}

func (s *Server) handleCronLint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Script string `json:"script"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	out, ok := cron.Lint(r.Context(), req.Script)
	writeJSON(w, http.StatusOK, map[string]any{"available": ok, "output": out})
}

// handleCronImportPreview reads the system crontabs (or a posted text) and
// returns the jobs it would create, without saving them.
func (s *Server) handleCronImport(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins import jobs"})
		return
	}
	var req struct {
		Text string `json:"text"`
		Save bool   `json:"save"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if req.Text == "" {
		req.Text = cron.ReadSystemCrontabs(r.Context())
	}
	jobs := cron.ParseCrontab(req.Text)
	if !req.Save {
		writeJSON(w, http.StatusOK, jobs)
		return
	}
	saved := []cron.Job{}
	for i := range jobs {
		if j, err := s.cron.Save(r.Context(), u.Username, &jobs[i]); err == nil {
			saved = append(saved, *j)
		}
	}
	_ = s.store.Audit(r.Context(), u.Username, "job.import", "", strconv.Itoa(len(saved))+" jobs")
	writeJSON(w, http.StatusOK, saved)
}

// handlePing is the unauthenticated heartbeat endpoint.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if !s.cron.Ping(r.Context(), r.PathValue("token")) {
		http.Error(w, "unknown heartbeat", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("OK\n"))
}
