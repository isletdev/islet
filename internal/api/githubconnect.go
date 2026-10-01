package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/github"
	"github.com/isletdev/islet/pkg/api"
)

// Connecting GitHub without opening a repository's settings.
//
// The thing this exists to delete is a sentence that used to appear in every
// deployment guide ever written: "now add a webhook to your repository, paste
// this URL, paste this secret, choose these events". Islet generated that
// secret per app and handed it over to be typed into GitHub by hand, for every
// application, forever.
//
// There are two ways out and both are here. A GitHub App carries one webhook
// for every repository it is installed on, so there is nothing per repository
// at all — and it is created in one click now, from a manifest this server
// writes, rather than from an eleven-field form. A token is the other way: with
// it Islet creates the webhook itself through the API, which a person also
// never sees.

// handleGitHubManifest hands the panel the app to ask GitHub for.
//
// The browser posts this to github.com itself, because that is how the flow
// works: the person has to be the one asking, on a page where they can see what
// they are agreeing to.
func (s *Server) handleGitHubManifest(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	state, err := s.github.StartManifest(r.Context())
	if err != nil {
		s.failed(w, "github", err)
		return
	}
	// A name has to be unique across all of GitHub, so it carries this server's
	// own name. The person can change it on GitHub's page before pressing the
	// button, which is the moment they are looking at it anyway.
	name := "Islet " + strings.TrimSuffix(s.store.Hostname, ".local")
	manifest := github.Manifest(name, s.publicURL(r))
	body, err := json.Marshal(manifest)
	if err != nil {
		s.failed(w, "github", err)
		return
	}
	org := strings.TrimSpace(r.URL.Query().Get("org"))
	post := "https://github.com/settings/apps/new?state=" + state
	if org != "" {
		post = "https://github.com/organizations/" + org + "/settings/apps/new?state=" + state
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"postUrl":  post,
		"manifest": string(body),
		"name":     name,
	})
}

// handleGitHubManifestCallback is where GitHub sends the browser back.
//
// It is a redirect rather than JSON because a browser is what arrives: the
// person pressed a button on github.com and is waiting to be somewhere useful.
func (s *Server) handleGitHubManifestCallback(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	q := r.URL.Query()
	cfg, err := s.github.ConvertManifest(r.Context(), q.Get("code"), q.Get("state"))
	if err != nil {
		http.Redirect(w, r, "/settings?github=failed&reason="+urlQueryEscape(err.Error()), http.StatusFound)
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "github.app.created", cfg.AppID, cfg.Slug)
	// Straight to the install page: an app that exists and is installed nowhere
	// can see no repositories, which looks exactly like a connection that did
	// not work.
	if u := s.github.InstallURL(r.Context()); u != "" {
		http.Redirect(w, r, u, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/settings?github=connected", http.StatusFound)
}

// handleGitHubToken stores or forgets the personal access token.
func (s *Server) handleGitHubToken(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	actor := userFrom(r.Context()).Username
	if r.Method == http.MethodDelete {
		s.github.ClearToken(r.Context())
		_ = s.store.Audit(r.Context(), actor, "github.token.clear", "", "")
		writeJSON(w, http.StatusOK, s.github.Account(r.Context()))
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	login, err := s.github.SaveToken(r.Context(), req.Token)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "github", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), actor, "github.token.save", login, "")
	writeJSON(w, http.StatusOK, s.github.Account(r.Context()))
}

// handleGitHubPublish turns a directory on this server into a repository.
func (s *Server) handleGitHubPublish(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Path        string `json:"path"`
		Name        string `json:"name"`
		Owner       string `json:"owner"`
		Description string `json:"description"`
		Private     bool   `json:"private"`
		Branch      string `json:"branch"`
		// CreateApp also makes an Islet app for it, deploying on every push.
		CreateApp bool   `json:"createApp"`
		AppName   string `json:"appName"`
		Domain    string `json:"domain"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	actor := userFrom(r.Context()).Username
	out, err := s.github.Publish(r.Context(), actor, req.Path, github.NewRepo{
		Owner:       req.Owner,
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		Private:     req.Private,
	}, req.Branch)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "github", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), actor, "github.publish", out.Repo.FullName, req.Path)

	res := map[string]any{"published": out}
	if req.CreateApp && s.deploy != nil {
		name := strings.TrimSpace(req.AppName)
		if name == "" {
			_, name, _ = strings.Cut(out.Repo.FullName, "/")
		}
		app := &deploy.App{
			Name:       name,
			Source:     "git",
			RepoURL:    out.Repo.URL,
			Branch:     out.Branch,
			Domain:     strings.TrimSpace(req.Domain),
			AutoDeploy: true,
		}
		saved, err := s.deploy.Save(r.Context(), app)
		if err != nil {
			res["appError"] = err.Error()
		} else {
			res["app"] = saved
			res["webhook"] = s.wireWebhook(r.Context(), r, actor, saved)
		}
	}
	writeJSON(w, http.StatusAccepted, res)
}

// wireWebhook makes sure a repository will tell this server about pushes, and
// says in words what it did — because "it is connected" and "somebody still has
// to go and do something" are the two answers, and only one of them is finished.
func (s *Server) wireWebhook(ctx context.Context, r *http.Request, actor string, a *deploy.App) string {
	full, ok := github.RepoFromURL(a.RepoURL)
	if !ok {
		return "not a GitHub repository, so nothing to wire"
	}
	if s.github.Configured(ctx) {
		if repos, err := s.github.Repos(ctx); err == nil {
			for _, repo := range repos {
				if strings.EqualFold(repo.FullName, full) {
					return "the GitHub App already delivers this repository's pushes; nothing to add"
				}
			}
		}
	}
	secret, err := s.github.HookSecret(ctx)
	if err != nil {
		return "could not read this server's hook secret: " + err.Error()
	}
	created, err := s.github.EnsureWebhook(ctx, full, strings.TrimRight(s.publicURL(r), "/")+"/api/v1/hooks/github", secret)
	if err != nil {
		return "could not add the webhook: " + err.Error()
	}
	_ = s.store.Audit(ctx, actor, "github.webhook", full, a.Name)
	if created {
		return "webhook added to " + full
	}
	return "webhook on " + full + " already pointed here; its secret was refreshed"
}

// handleGitHubWire adds the webhook for an app that already exists, for the
// case where a connection was made after the app was.
func (s *Server) handleGitHubWire(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) || s.deploy == nil {
		return
	}
	a, err := s.deploy.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such app"})
		return
	}
	note := s.wireWebhook(r.Context(), r, userFrom(r.Context()).Username, a)
	writeJSON(w, http.StatusOK, map[string]string{"result": note})
}

func urlQueryEscape(s string) string {
	return strings.NewReplacer(" ", "+", "&", "%26", "#", "%23", "?", "%3F").Replace(s)
}
