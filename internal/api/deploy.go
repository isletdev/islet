package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) deployErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploy.ErrNotFound):
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
	case errors.Is(err, deploy.ErrBusy):
		writeJSON(w, http.StatusConflict, api.Error{Error: "busy", Message: err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
	}
}

func maskApp(a *deploy.App, role string) {
	if role == "admin" {
		return
	}
	a.WebhookSecret = ""
	var lines []string
	for _, l := range strings.Split(a.Env, "\n") {
		if k, _, ok := strings.Cut(l, "="); ok {
			lines = append(lines, k+"=••••")
		}
	}
	a.Env = strings.Join(lines, "\n")
	if strings.Contains(a.RepoURL, "@") {
		a.RepoURL = "(hidden)"
	}
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.deploy.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	role := userFrom(r.Context()).Role
	for i := range list {
		maskApp(&list[i], role)
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.deploy.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.deployErr(w, err)
		return
	}
	maskApp(a, userFrom(r.Context()).Role)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleAppSave(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins configure apps"})
		return
	}
	a := deploy.App{AutoDeploy: true}
	if err := decode(r, &a); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	a.ID = r.PathValue("id")
	saved, err := s.deploy.Save(r.Context(), &a)
	if err != nil {
		s.deployErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.save", saved.ID, saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins delete apps"})
		return
	}
	if err := s.deploy.Delete(r.Context(), u.Username, r.PathValue("id")); err != nil {
		s.deployErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.delete", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

// handleAppInspect clones a repo and reports what Islet detected.
func (s *Server) handleAppInspect(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins configure apps"})
		return
	}
	var req struct{ RepoURL, Branch, RootDir string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if req.Branch == "" {
		req.Branch = "main"
	}
	d, err := s.deploy.Inspect(r.Context(), req.RepoURL, req.Branch, req.RootDir)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "clone", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleAppDeploy starts a deploy (or rollback with ?release=ID) and
// streams the log as SSE until it ends.
func (s *Server) handleAppDeploy(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot deploy"})
		return
	}
	id := r.PathValue("id")
	trigger := "manual"
	var rollback int64
	if v := r.URL.Query().Get("release"); v != "" {
		rollback, _ = strconv.ParseInt(v, 10, 64)
		trigger = "rollback"
	} else if r.URL.Query().Get("redeploy") == "1" {
		trigger = "redeploy"
	}
	rel, err := s.deploy.Deploy(r.Context(), u.Username, id, trigger, rollback)
	if err != nil {
		s.deployErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.deploy", id, fmt.Sprintf("release #%d (%s)", rel.Number, trigger))
	s.streamDeploy(w, r, id)
}

// handleAppPromote deploys the source app's live image onto another app.
func (s *Server) handleAppPromote(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot deploy"})
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	rel, err := s.deploy.Promote(r.Context(), u.Username, r.PathValue("id"), req.To)
	if err != nil {
		s.deployErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.promote", req.To, fmt.Sprintf("release #%d from %s", rel.Number, r.PathValue("id")))
	s.streamDeploy(w, r, req.To)
}

// handleAppDeployLog attaches to a running deploy.
func (s *Server) handleAppDeployLog(w http.ResponseWriter, r *http.Request) {
	s.streamDeploy(w, r, r.PathValue("id"))
}

func (s *Server) streamDeploy(w http.ResponseWriter, r *http.Request, id string) {
	past, ch, unsub := s.deploy.Subscribe(id)
	defer unsub()
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for _, l := range past {
			fmt.Fprintln(pw, l)
		}
		if ch == nil {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case l, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintln(pw, l)
			}
		}
	}()
	streamLines(w, r, pr, func() error {
		pr.Close()
		a, err := s.deploy.Get(r.Context(), id)
		if err != nil {
			return err
		}
		if a.Last != nil && (a.Last.Status == "failed" || a.Last.Status == "cancelled") {
			return errors.New(a.Last.Error)
		}
		return nil
	})
}

func (s *Server) handleAppCancel(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot cancel deploys"})
		return
	}
	if !s.deploy.Cancel(r.PathValue("id")) {
		writeJSON(w, http.StatusConflict, api.Error{Error: "idle", Message: "no deploy is running"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAppReleases(w http.ResponseWriter, r *http.Request) {
	list, err := s.deploy.Releases(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAppRelease(w http.ResponseWriter, r *http.Request) {
	rid, _ := strconv.ParseInt(r.PathValue("release"), 10, 64)
	rel, err := s.deploy.Release(r.Context(), r.PathValue("id"), rid)
	if err != nil {
		s.deployErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

// handleDeployHook is the unauthenticated push webhook. GitHub, GitLab and
// Gitea payloads are accepted; the secret is per app.
func (s *Server) handleDeployHook(w http.ResponseWriter, r *http.Request) {
	a, err := s.deploy.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "unknown app", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if !deploy.VerifyWebhook(a.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256"), r.Header.Get("X-Gitlab-Token"), r.Header.Get("X-Gitea-Signature")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	if ev := r.Header.Get("X-GitHub-Event"); ev == "ping" {
		w.Write([]byte("pong\n"))
		return
	}
	var payload struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(body, &payload)
	if payload.Ref != "" && !deploy.RefMatches(a.Branch, payload.Ref) {
		w.Write([]byte("ignored: ref does not match " + a.Branch + "\n"))
		return
	}
	if !a.AutoDeploy {
		w.Write([]byte("ignored: auto-deploy is off\n"))
		return
	}
	trigger := "push"
	if a.DeployOn == "ci" {
		if payload.Ref != "" {
			w.Write([]byte("ignored: this app deploys after CI passes; call this hook from the CI job (no push payload)\n"))
			return
		}
		trigger = "ci"
	}
	ref := ""
	if strings.HasPrefix(payload.Ref, "refs/tags/") {
		ref = deploy.RefName(payload.Ref)
	}
	if _, err := s.deploy.DeployRef(r.Context(), "webhook", a.ID, trigger, 0, ref); err != nil {
		if errors.Is(err, deploy.ErrBusy) {
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte("a deploy is already running\n"))
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.store.Audit(r.Context(), "webhook", "app.deploy", a.ID, trigger)
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("deploy started\n"))
}
