// Package github talks to the GitHub API as a GitHub App: repository
// listing for the app picker, installation tokens for cloning private
// repositories, and registration tokens for self-hosted runners. Nothing
// long-lived is stored except the app's own private key, encrypted.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

// Config is what the maintainer pastes from the GitHub App page.
type Config struct {
	AppID         string `json:"appId"`
	ClientID      string `json:"clientId"`
	PrivateKey    string `json:"privateKey,omitempty"`
	WebhookSecret string `json:"webhookSecret,omitempty"`
	Slug          string `json:"slug"` // app slug for the install link
	Configured    bool   `json:"configured"`
}

// Installation is one account the app is installed on.
type Installation struct {
	ID      int64  `json:"id"`
	Account string `json:"account"`
	Type    string `json:"type"`
}

// Repo is a repository the app can see.
type Repo struct {
	FullName      string `json:"fullName"`
	DefaultBranch string `json:"defaultBranch"`
	Private       bool   `json:"private"`
	URL           string `json:"url"`
	Installation  int64  `json:"installation"`
}

// Client holds the app credentials.
type Client struct {
	st   *store.Store
	keys *auth.Keys
	http *http.Client

	mu     sync.Mutex
	tokens map[int64]cachedToken
}

type cachedToken struct {
	token string
	exp   time.Time
}

// New builds the client.
func New(st *store.Store, keys *auth.Keys) *Client {
	return &Client{st: st, keys: keys, http: &http.Client{Timeout: 30 * time.Second}, tokens: map[int64]cachedToken{}}
}

// Load reads the stored config (secrets decrypted).
func (c *Client) Load(ctx context.Context) Config {
	var cfg Config
	cfg.AppID, _, _ = c.st.Setting(ctx, "github.app_id")
	cfg.ClientID, _, _ = c.st.Setting(ctx, "github.client_id")
	cfg.Slug, _, _ = c.st.Setting(ctx, "github.slug")
	if v, ok, _ := c.st.Setting(ctx, "github.private_key"); ok && v != "" {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil {
			if p, err := c.keys.Decrypt(b); err == nil {
				cfg.PrivateKey = string(p)
			}
		}
	}
	if v, ok, _ := c.st.Setting(ctx, "github.webhook_secret"); ok && v != "" {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil {
			if p, err := c.keys.Decrypt(b); err == nil {
				cfg.WebhookSecret = string(p)
			}
		}
	}
	cfg.Configured = cfg.AppID != "" && cfg.PrivateKey != ""
	return cfg
}

// Configured reports whether an app is set up.
func (c *Client) Configured(ctx context.Context) bool { return c.Load(ctx).Configured }

// Save validates and stores the config. Empty secrets keep the old values.
func (c *Client) Save(ctx context.Context, cfg Config) error {
	old := c.Load(ctx)
	if cfg.PrivateKey == "" {
		cfg.PrivateKey = old.PrivateKey
	}
	if cfg.WebhookSecret == "" {
		cfg.WebhookSecret = old.WebhookSecret
	}
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	if _, err := strconv.ParseInt(cfg.AppID, 10, 64); err != nil {
		return errors.New("App ID must be a number (shown at the top of the GitHub App page)")
	}
	if _, err := parseKey(cfg.PrivateKey); err != nil {
		return errors.New("private key must be the .pem file GitHub generated: " + err.Error())
	}
	seal := func(v string) string {
		b, _ := c.keys.Encrypt([]byte(v))
		return base64.StdEncoding.EncodeToString(b)
	}
	for k, v := range map[string]string{"github.app_id": cfg.AppID, "github.client_id": strings.TrimSpace(cfg.ClientID), "github.slug": strings.TrimSpace(cfg.Slug), "github.private_key": seal(cfg.PrivateKey), "github.webhook_secret": seal(cfg.WebhookSecret)} {
		if err := c.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.tokens = map[int64]cachedToken{}
	c.mu.Unlock()
	// Prove the key works.
	if _, err := c.Installations(ctx); err != nil {
		return errors.New("saved, but GitHub rejected the credentials: " + err.Error())
	}
	return nil
}

// Clear removes the configuration.
func (c *Client) Clear(ctx context.Context) {
	for _, k := range []string{"github.app_id", "github.client_id", "github.slug", "github.private_key", "github.webhook_secret"} {
		_ = c.st.SetSetting(ctx, k, "")
	}
}

func parseKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemText)))
	if block == nil {
		return nil, errors.New("not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}

// jwt mints a ten-minute app JWT (RS256).
func (c *Client) jwt(ctx context.Context) (string, error) {
	cfg := c.Load(ctx)
	if !cfg.Configured {
		return "", errors.New("GitHub App is not configured")
	}
	key, err := parseKey(cfg.PrivateKey)
	if err != nil {
		return "", err
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	head := enc(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims := enc(map[string]any{"iat": now - 30, "exp": now + 540, "iss": cfg.AppID})
	signing := head + "." + claims
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (c *Client) call(ctx context.Context, bearer, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "islet")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct{ Message string }
		_ = json.Unmarshal(b, &e)
		return fmt.Errorf("GitHub %s %s: %d %s", method, path, res.StatusCode, e.Message)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Installations lists where the app is installed.
func (c *Client) Installations(ctx context.Context) ([]Installation, error) {
	tok, err := c.jwt(ctx)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	}
	if err := c.call(ctx, tok, "GET", "/app/installations?per_page=100", nil, &raw); err != nil {
		return nil, err
	}
	out := []Installation{}
	for _, r := range raw {
		out = append(out, Installation{ID: r.ID, Account: r.Account.Login, Type: r.Account.Type})
	}
	return out, nil
}

// InstallationToken returns a cached or fresh one-hour token.
func (c *Client) InstallationToken(ctx context.Context, id int64) (string, error) {
	c.mu.Lock()
	if t, ok := c.tokens[id]; ok && time.Until(t.exp) > 2*time.Minute {
		c.mu.Unlock()
		return t.token, nil
	}
	c.mu.Unlock()
	jwt, err := c.jwt(ctx)
	if err != nil {
		return "", err
	}
	var res struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.call(ctx, jwt, "POST", "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens", map[string]any{}, &res); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[id] = cachedToken{token: res.Token, exp: res.ExpiresAt}
	c.mu.Unlock()
	return res.Token, nil
}

// Repos lists every repository across installations.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	insts, err := c.Installations(ctx)
	if err != nil {
		return nil, err
	}
	out := []Repo{}
	for _, in := range insts {
		tok, err := c.InstallationToken(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		for page := 1; page <= 10; page++ {
			var res struct {
				Repositories []struct {
					FullName      string `json:"full_name"`
					DefaultBranch string `json:"default_branch"`
					Private       bool   `json:"private"`
					CloneURL      string `json:"clone_url"`
				} `json:"repositories"`
			}
			if err := c.call(ctx, tok, "GET", "/installation/repositories?per_page=100&page="+strconv.Itoa(page), nil, &res); err != nil {
				return nil, err
			}
			for _, r := range res.Repositories {
				out = append(out, Repo{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private, URL: r.CloneURL, Installation: in.ID})
			}
			if len(res.Repositories) < 100 {
				break
			}
		}
	}
	return out, nil
}

// RepoFromURL extracts owner/name from a github.com URL.
func RepoFromURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		if strings.HasPrefix(raw, "git@github.com:") {
			return strings.TrimSuffix(strings.TrimPrefix(raw, "git@github.com:"), ".git"), true
		}
		return "", false
	}
	p := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	if strings.Count(p, "/") != 1 {
		return "", false
	}
	return p, true
}

// CloneURL returns an authenticated https clone URL for a GitHub repo the
// app can see, or ok=false when the app is not installed there.
func (c *Client) CloneURL(ctx context.Context, repoURL string) (string, bool) {
	full, ok := RepoFromURL(repoURL)
	if !ok || !c.Configured(ctx) {
		return "", false
	}
	jwt, err := c.jwt(ctx)
	if err != nil {
		return "", false
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, jwt, "GET", "/repos/"+full+"/installation", nil, &inst); err != nil {
		return "", false
	}
	tok, err := c.InstallationToken(ctx, inst.ID)
	if err != nil {
		return "", false
	}
	return "https://x-access-token:" + tok + "@github.com/" + full + ".git", true
}

// RunnerToken mints a registration token for a repository or organisation URL.
func (c *Client) RunnerToken(ctx context.Context, scopeURL string) (string, error) {
	u, err := url.Parse(scopeURL)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	jwt, err := c.jwt(ctx)
	if err != nil {
		return "", err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	path := "/orgs/" + parts[0] + "/installation"
	regPath := "/orgs/" + parts[0] + "/actions/runners/registration-token"
	if len(parts) == 2 {
		path = "/repos/" + parts[0] + "/" + parts[1] + "/installation"
		regPath = "/repos/" + parts[0] + "/" + parts[1] + "/actions/runners/registration-token"
	}
	if err := c.call(ctx, jwt, "GET", path, nil, &inst); err != nil {
		return "", err
	}
	tok, err := c.InstallationToken(ctx, inst.ID)
	if err != nil {
		return "", err
	}
	var res struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, tok, "POST", regPath, map[string]any{}, &res); err != nil {
		return "", err
	}
	return res.Token, nil
}

// AppInfo is what GitHub says this App may do.
//
// It exists because the errors on the other side of a missing permission are
// unactionable: "Resource not accessible by integration" with a 403, over and
// over in a log, while the panel says the App is configured and installed —
// which it is. What it is not is *allowed*, and only this endpoint says which
// of the permissions Islet needs are absent.
type AppInfo struct {
	Name        string            `json:"name"`
	Permissions map[string]string `json:"permissions"`
	Events      []string          `json:"events"`
}

// Need is one permission or event Islet uses, and what stops working without it.
type Need struct {
	Kind  string `json:"kind"`  // permission | event
	Name  string `json:"name"`  // contents, metadata, organization_self_hosted_runners, push…
	Level string `json:"level"` // read | write, empty for an event
	Label string `json:"label"` // what to tick, in GitHub's own words
	For   string `json:"for"`   // what it is for, in Islet's
}

// Needs is everything the App is asked to carry. Order is the order of the
// sections on GitHub's permissions page, so somebody fixing this reads down the
// list once rather than hunting.
var Needs = []Need{
	{Kind: "permission", Name: "metadata", Level: "read", Label: "Repository → Metadata (read)", For: "listing the repositories you can deploy"},
	{Kind: "permission", Name: "contents", Level: "read", Label: "Repository → Contents (read)", For: "cloning private repositories without a token"},
	{Kind: "permission", Name: "administration", Level: "write", Label: "Repository → Administration (read and write)", For: "registering a runner for one repository"},
	{Kind: "permission", Name: "organization_self_hosted_runners", Level: "write", Label: "Organization → Self-hosted runners (read and write)", For: "registering runners for a whole organisation"},
	{Kind: "event", Name: "push", Label: "Subscribe to events → Push", For: "deploying when you push"},
	{Kind: "event", Name: "workflow_job", Label: "Subscribe to events → Workflow job", For: "starting a runner when a job queues"},
}

// permLevel finds a permission whichever way GitHub spelled it.
//
// The key vocabulary is not consistent about organisation permissions: the
// documentation names one of them "organization_self_hosted_runners" and
// another plain "members", and a public App carrying the first could not be
// found to check against. Both spellings are accepted, because the cost of
// guessing wrong is a diagnostic that tells somebody to grant a permission they
// have already granted — which is worse than saying nothing at all.
func permLevel(info *AppInfo, name string) string {
	for _, k := range []string{name, "organization_" + name, strings.TrimPrefix(name, "organization_")} {
		if v, ok := info.Permissions[k]; ok {
			return v
		}
	}
	return ""
}

// RepoCount is how many repositories every installation together can see.
//
// An installation that was granted access to nothing is indistinguishable, from
// the repository picker, from an App that was never installed: an empty list
// either way. One call per installation, asking for a single row, is enough to
// tell those two apart and say so.
func (c *Client) RepoCount(ctx context.Context) (int, error) {
	insts, err := c.Installations(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, in := range insts {
		tok, err := c.InstallationToken(ctx, in.ID)
		if err != nil {
			return total, err
		}
		var res struct {
			TotalCount int `json:"total_count"`
		}
		if err := c.call(ctx, tok, "GET", "/installation/repositories?per_page=1", nil, &res); err != nil {
			return total, err
		}
		total += res.TotalCount
	}
	return total, nil
}

// App reads the App's own record, which is the only place its permissions are
// written down.
func (c *Client) App(ctx context.Context) (*AppInfo, error) {
	tok, err := c.jwt(ctx)
	if err != nil {
		return nil, err
	}
	var info AppInfo
	if err := c.call(ctx, tok, "GET", "/app", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Missing lists what Islet needs and the App does not carry.
//
// A permission granted at "write" satisfies a need for "read": GitHub's levels
// are a ladder, and somebody who ticked more than was asked for should not be
// told they ticked too little.
func Missing(info *AppInfo) []Need {
	if info == nil {
		return nil
	}
	out := []Need{}
	for _, n := range Needs {
		if n.Kind == "event" {
			if !slices.Contains(info.Events, n.Name) {
				out = append(out, n)
			}
			continue
		}
		got := permLevel(info, n.Name)
		if got == "write" || got == "admin" || (got == "read" && n.Level == "read") {
			continue
		}
		out = append(out, n)
	}
	return out
}
