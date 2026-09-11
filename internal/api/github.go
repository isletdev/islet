package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/github"
	"github.com/isletdev/islet/internal/runner"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleGitHubConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.github.Load(r.Context())
	cfg.PrivateKey, cfg.WebhookSecret = "", ""
	out := map[string]any{"config": cfg, "hookUrl": "/api/v1/hooks/github"}
	if cfg.Configured {
		if insts, err := s.github.Installations(r.Context()); err == nil {
			out["installations"] = insts
		} else {
			out["error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGitHubSave(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var cfg github.Config
	if err := decode(r, &cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if cfg.AppID == "" && cfg.PrivateKey == "" {
		s.github.Clear(r.Context())
		_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "github.clear", "", "")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.github.Save(r.Context(), cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "github", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "github.save", cfg.AppID, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	if !s.github.Configured(r.Context()) {
		writeJSON(w, http.StatusOK, []github.Repo{})
		return
	}
	repos, err := s.github.Repos(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "github", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

// handleGitHubHook is the single webhook a GitHub App delivers to: pushes
// deploy matching apps, workflow_job events scale matching runner pools.
func (s *Server) handleGitHubHook(w http.ResponseWriter, r *http.Request) {
	cfg := s.github.Load(r.Context())
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if cfg.WebhookSecret == "" || !runner.VerifyGitHub(cfg.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var ev struct {
		Ref         string `json:"ref"`
		Action      string `json:"action"`
		WorkflowRun struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			HeadBranch string `json:"head_branch"`
		} `json:"workflow_run"`
		Repository struct {
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(body, &ev)
	full := strings.ToLower(ev.Repository.FullName)
	var notes []string
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		_, _ = w.Write([]byte("pong\n"))
		return
	case "push":
		apps, _ := s.deploy.List(r.Context())
		for _, a := range apps {
			if rep, ok := github.RepoFromURL(a.RepoURL); ok && strings.ToLower(rep) == full && deploy.RefMatches(a.Branch, ev.Ref) && a.AutoDeploy {
				if a.DeployOn == "ci" {
					notes = append(notes, a.Name+": waiting for CI")
					continue
				}
				ref := ""
				if strings.HasPrefix(ev.Ref, "refs/tags/") {
					ref = deploy.RefName(ev.Ref)
				}
				if _, err := s.deploy.DeployRef(r.Context(), "github", a.ID, "push", 0, ref); err != nil {
					notes = append(notes, a.Name+": "+err.Error())
				} else {
					_ = s.store.Audit(r.Context(), "github", "app.deploy", a.ID, "push")
					notes = append(notes, a.Name+": deploy started")
				}
			}
		}
	case "workflow_run":
		if ev.Action != "completed" || ev.WorkflowRun.Conclusion != "success" {
			_, _ = w.Write([]byte("ignored: workflow " + ev.Action + " " + ev.WorkflowRun.Conclusion + "\n"))
			return
		}
		apps, _ := s.deploy.List(r.Context())
		for _, a := range apps {
			if rep, ok := github.RepoFromURL(a.RepoURL); ok && strings.ToLower(rep) == full && a.DeployOn == "ci" && a.AutoDeploy && deploy.RefMatches(a.Branch, "refs/heads/"+ev.WorkflowRun.HeadBranch) {
				if _, err := s.deploy.DeployRef(r.Context(), "github", a.ID, "ci", 0, ""); err != nil {
					notes = append(notes, a.Name+": "+err.Error())
				} else {
					_ = s.store.Audit(r.Context(), "github", "app.deploy", a.ID, "ci: "+ev.WorkflowRun.Name)
					notes = append(notes, a.Name+": deploy started after "+ev.WorkflowRun.Name)
				}
			}
		}
	case "workflow_job":
		pools, _ := s.runners.List(r.Context())
		for i := range pools {
			p := &pools[i]
			if p.Provider != "github" {
				continue
			}
			u, _ := url.Parse(p.URL)
			scope := strings.ToLower(strings.Trim(u.Path, "/"))
			if scope == full || scope == strings.ToLower(ev.Repository.Owner.Login) {
				msg, err := s.runners.HandleWorkflowJob(r.Context(), p, body)
				if err != nil {
					notes = append(notes, p.Name+": "+err.Error())
				} else {
					notes = append(notes, p.Name+": "+msg)
				}
			}
		}
	default:
		_, _ = w.Write([]byte("ignored\n"))
		return
	}
	if len(notes) == 0 {
		notes = []string{"nothing matched " + full}
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(strings.Join(notes, "\n") + "\n"))
}

var _ = errors.New
