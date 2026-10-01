package github

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Being an account rather than being an app.
//
// The App credentials next door answer "which repositories may this server
// read", which is the right question for connecting something that already
// exists. They are the wrong credential for two things people want anyway: an
// installation token cannot create a repository under a personal account, and
// it cannot push. Both of those are the account's own authority, so there is a
// second credential here — one token, stored once, sealed like every other.
//
// Neither is required and both may be present. Whichever can answer a question
// answers it; where both can, the App wins, because its token is minted per
// hour and scoped to the installation rather than to everything the person can
// reach.

const (
	settingToken      = "github.token"
	settingTokenLogin = "github.token_login"
	settingHookSecret = "github.hook_secret"
)

// Account is who Islet is on GitHub, and how.
type Account struct {
	// Login is the user the token belongs to, empty when there is no token.
	Login string `json:"login,omitempty"`
	// App is true when an App is configured, which is the no-secrets-per-repo
	// half of this.
	App bool `json:"app"`
	// AppSlug is for the install link.
	AppSlug string `json:"appSlug,omitempty"`
	// Installs is how many accounts the App is installed on. Zero with an App
	// configured means it exists and has been installed nowhere, which looks
	// exactly like a broken connection and is not one.
	Installs int `json:"installs"`
	// CanCreateRepos says whether "publish this to GitHub" is available, which
	// needs the token rather than the App.
	CanCreateRepos bool `json:"canCreateRepos"`
}

// SaveToken stores a personal access token after checking that GitHub accepts
// it, and returns who it belongs to.
//
// Checked rather than taken on faith because a token is pasted by hand, usually
// from a page that also shows three other strings, and the failure of an
// unchecked one happens later and somewhere else.
func (c *Client) SaveToken(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("paste a token")
	}
	var who struct {
		Login string `json:"login"`
	}
	if err := c.call(ctx, token, "GET", "/user", nil, &who); err != nil {
		return "", fmt.Errorf("GitHub would not accept that token: %w", err)
	}
	if who.Login == "" {
		return "", errors.New("GitHub accepted that token but would not say who it belongs to")
	}
	sealed, err := c.keys.Encrypt([]byte(token))
	if err != nil {
		return "", err
	}
	if err := c.st.SetSetting(ctx, settingToken, base64.StdEncoding.EncodeToString(sealed)); err != nil {
		return "", err
	}
	if err := c.st.SetSetting(ctx, settingTokenLogin, who.Login); err != nil {
		return "", err
	}
	return who.Login, nil
}

// Token is the stored personal access token, or empty.
func (c *Client) Token(ctx context.Context) string {
	v, ok, _ := c.st.Setting(ctx, settingToken)
	if !ok || v == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return ""
	}
	p, err := c.keys.Decrypt(b)
	if err != nil {
		return ""
	}
	return string(p)
}

// ClearToken forgets it.
func (c *Client) ClearToken(ctx context.Context) {
	_ = c.st.SetSetting(ctx, settingToken, "")
	_ = c.st.SetSetting(ctx, settingTokenLogin, "")
}

// Account describes the connection as the panel shows it.
func (c *Client) Account(ctx context.Context) Account {
	a := Account{}
	cfg := c.Load(ctx)
	a.App, a.AppSlug = cfg.Configured, cfg.Slug
	if a.App {
		if insts, err := c.Installations(ctx); err == nil {
			a.Installs = len(insts)
		}
	}
	a.Login, _, _ = c.st.Setting(ctx, settingTokenLogin)
	a.CanCreateRepos = c.Token(ctx) != ""
	return a
}

// ---- repositories ---------------------------------------------------------

// TokenRepos lists what the token can see, newest first.
//
// Separate from Repos, which asks the App. A person may have both, and the two
// lists overlap; the caller merges them because only it knows which it wants to
// show.
func (c *Client) TokenRepos(ctx context.Context) ([]Repo, error) {
	tok := c.Token(ctx)
	if tok == "" {
		return nil, nil
	}
	out := []Repo{}
	for page := 1; page <= 5; page++ {
		var raw []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
			CloneURL      string `json:"clone_url"`
		}
		if err := c.call(ctx, tok, "GET",
			fmt.Sprintf("/user/repos?per_page=100&sort=pushed&page=%d", page), nil, &raw); err != nil {
			return out, err
		}
		for _, r := range raw {
			out = append(out, Repo{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private, URL: r.CloneURL})
		}
		if len(raw) < 100 {
			break
		}
	}
	return out, nil
}

// NewRepo is what to create.
type NewRepo struct {
	// Owner is a user or an organisation. Empty means the token's own account.
	Owner       string
	Name        string
	Description string
	Private     bool
}

// CreateRepo makes a repository and returns it.
//
// The token is used rather than the App: `POST /user/repos` is the account's
// own authority and an installation token does not have it. An organisation
// could be done either way; it is done this way too, so that there is one
// answer to "why did that work and this not".
func (c *Client) CreateRepo(ctx context.Context, in NewRepo) (Repo, error) {
	tok := c.Token(ctx)
	if tok == "" {
		return Repo{}, errors.New("creating a repository needs a GitHub token; add one under Settings → GitHub")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Repo{}, errors.New("a repository needs a name")
	}
	path := "/user/repos"
	if owner := strings.TrimSpace(in.Owner); owner != "" && !strings.EqualFold(owner, c.tokenLogin(ctx)) {
		path = "/orgs/" + url.PathEscape(owner) + "/repos"
	}
	body := map[string]any{
		"name":        name,
		"description": in.Description,
		"private":     in.Private,
		// No README, no licence, no .gitignore: the directory being published
		// is the repository's first commit, and an initial commit on the remote
		// is a conflict to resolve before the first push rather than a feature.
		"auto_init": false,
	}
	var raw struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		CloneURL      string `json:"clone_url"`
		HTMLURL       string `json:"html_url"`
	}
	if err := c.call(ctx, tok, "POST", path, body, &raw); err != nil {
		return Repo{}, err
	}
	return Repo{FullName: raw.FullName, DefaultBranch: raw.DefaultBranch, Private: raw.Private, URL: raw.CloneURL}, nil
}

func (c *Client) tokenLogin(ctx context.Context) string {
	v, _, _ := c.st.Setting(ctx, settingTokenLogin)
	return v
}

// ---- webhooks -------------------------------------------------------------

// EnsureWebhook makes sure a repository tells this server about pushes, and
// reports whether it had to add one.
//
// This is the whole of what somebody used to do by hand, per repository, for
// every application they deployed: open the settings, paste a URL, paste a
// secret, choose the events. With an App configured none of it is needed at
// all — the App carries one webhook for every repository it is installed on —
// so this is only reached when the connection is a token.
func (c *Client) EnsureWebhook(ctx context.Context, fullName, deliverTo, secret string) (bool, error) {
	tok := c.Token(ctx)
	if tok == "" {
		return false, errors.New("adding a webhook needs a GitHub token")
	}
	owner, repo, ok := splitRepo(fullName)
	if !ok {
		return false, fmt.Errorf("%q is not owner/repo", fullName)
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/hooks"

	var existing []struct {
		ID     int64 `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := c.call(ctx, tok, "GET", base+"?per_page=100", nil, &existing); err != nil {
		return false, err
	}
	for _, h := range existing {
		if strings.EqualFold(h.Config.URL, deliverTo) {
			// Already pointed here. The secret is not readable back from
			// GitHub, so it is rewritten rather than compared — saving the same
			// app twice must not leave a hook signed with a secret this server
			// has forgotten.
			err := c.call(ctx, tok, "PATCH", fmt.Sprintf("%s/%d", base, h.ID), map[string]any{
				"active": true,
				"events": []string{"push"},
				"config": hookConfig(deliverTo, secret),
			}, nil)
			return false, err
		}
	}
	return true, c.call(ctx, tok, "POST", base, map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": hookConfig(deliverTo, secret),
	}, nil)
}

func hookConfig(deliverTo, secret string) map[string]any {
	return map[string]any{
		"url":          deliverTo,
		"content_type": "json",
		"secret":       secret,
		"insecure_ssl": "0",
	}
}

// PushCredential is a token that may write to this repository.
//
// The App's installation token is preferred where it covers the repository: it
// lasts an hour and reaches only what the App was installed on. The personal
// token is the fallback, and the only thing that works for a repository the App
// has never been installed on — which includes every repository created a
// second ago.
func (c *Client) PushCredential(ctx context.Context, fullName string) (user, token string, err error) {
	if c.Configured(ctx) {
		if t, ok := c.installationFor(ctx, fullName); ok {
			return "x-access-token", t, nil
		}
	}
	if tok := c.Token(ctx); tok != "" {
		return c.tokenLogin(ctx), tok, nil
	}
	return "", "", errors.New("no GitHub connection that can push; add an App or a token under Settings → GitHub")
}

// installationFor finds the installation token covering a repository.
func (c *Client) installationFor(ctx context.Context, fullName string) (string, bool) {
	repos, err := c.Repos(ctx)
	if err != nil {
		return "", false
	}
	for _, r := range repos {
		if strings.EqualFold(r.FullName, fullName) && r.Installation != 0 {
			tok, err := c.InstallationToken(ctx, r.Installation)
			if err != nil {
				return "", false
			}
			return tok, true
		}
	}
	return "", false
}

// splitRepo takes owner/repo out of a full name or a URL.
func splitRepo(s string) (owner, repo string, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "github.com"); i >= 0 {
		s = strings.Trim(s[i+len("github.com"):], "/:")
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", false
	}
	return parts[len(parts)-2], parts[len(parts)-1], true
}

// HookSecret is the secret every webhook Islet creates is signed with.
//
// One per server rather than one per repository, which is the whole point: a
// person never sees it, never pastes it, and never has a repository's settings
// page open. A GitHub App needs none of this — its own webhook covers every
// repository it is installed on — so this is for the token connection, and the
// receiver accepts either.
func (c *Client) HookSecret(ctx context.Context) (string, error) {
	if v, ok, _ := c.st.Setting(ctx, settingHookSecret); ok && v != "" {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil {
			if p, err := c.keys.Decrypt(b); err == nil {
				return string(p), nil
			}
		}
	}
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(b[:])
	sealed, err := c.keys.Encrypt([]byte(secret))
	if err != nil {
		return "", err
	}
	if err := c.st.SetSetting(ctx, settingHookSecret, base64.StdEncoding.EncodeToString(sealed)); err != nil {
		return "", err
	}
	return secret, nil
}
