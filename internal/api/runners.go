package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/isletdev/islet/internal/runner"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) runnerErr(w http.ResponseWriter, err error) {
	if errors.Is(err, runner.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	s.failed(w, "invalid", err)
}

func maskPool(p *runner.Pool, role string) {
	p.Token = ""
	if role != "admin" {
		p.WebhookSecret = ""
	}
}

func (s *Server) handleRunnerPools(w http.ResponseWriter, r *http.Request) {
	list, err := s.runners.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	role := userFrom(r.Context()).Role
	for i := range list {
		maskPool(&list[i], role)
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRunnerPoolSave(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins manage runners"})
		return
	}
	p := runner.Pool{Enabled: true, MinIdle: 1, MaxRunners: 2}
	if err := decode(r, &p); err != nil {
		s.badJSON(w, err)
		return
	}
	p.ID = r.PathValue("id")
	saved, err := s.runners.Save(r.Context(), &p)
	if err != nil {
		s.runnerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "runner.save", saved.ID, saved.Name)
	maskPool(saved, u.Role)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleRunnerPoolDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins manage runners"})
		return
	}
	if err := s.runners.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.runnerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "runner.delete", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunnerJobs(w http.ResponseWriter, r *http.Request) {
	list, err := s.runners.Jobs(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRunnerWorkflow(w http.ResponseWriter, r *http.Request) {
	p, err := s.runners.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.runnerErr(w, err)
		return
	}
	app := r.URL.Query().Get("app")
	if app == "" {
		app = "my-app"
	}
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = "main"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(runner.DeployWorkflow(app, branch, p.Labels)))
}

// handleRunnerHook receives GitHub workflow_job events (unauthenticated,
// signed with the pool's secret).
func (s *Server) handleRunnerHook(w http.ResponseWriter, r *http.Request) {
	p, err := s.runners.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "unknown pool", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if !runner.VerifyGitHub(p.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		_, _ = w.Write([]byte("pong\n"))
	case "workflow_job":
		msg, err := s.runners.HandleWorkflowJob(r.Context(), p, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(msg + "\n"))
	default:
		_, _ = w.Write([]byte("ignored\n"))
	}
}
