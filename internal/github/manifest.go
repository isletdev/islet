package github

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Creating the GitHub App without anybody filling in a form.
//
// A GitHub App is the credential that makes "connect a repository" need no
// secret in the repository: one app, one webhook, every repository it is
// installed on. The cost has always been the registration — eleven fields, two
// permission grids, a private key to download and five values to paste back —
// which is why the instruction to do it sat in NEEDED_FROM_YOU for months and
// nobody did.
//
// GitHub has a flow for exactly this. The server describes the app it wants as
// a manifest, the person's browser posts it to github.com, they press one
// button, and GitHub sends back a temporary code which this server exchanges
// for the app id, the private key and the webhook secret. Nothing is typed and
// nothing is pasted.

const settingManifestState = "github.manifest_state"

// Manifest is the app Islet asks GitHub to create.
//
// The permissions are the ones the features here actually use, and no more:
// contents to clone, metadata because GitHub requires it alongside anything
// else, webhooks so a repository can be wired without opening its settings,
// administration and self-hosted runners for the runner pools, actions to read
// a workflow's result for deploy-on-green.
func Manifest(name, publicURL string) map[string]any {
	base := strings.TrimRight(publicURL, "/")
	return map[string]any{
		"name":            name,
		"url":             base,
		"redirect_url":    base + "/api/v1/github/manifest/callback",
		"hook_attributes": map[string]any{"url": base + "/api/v1/hooks/github", "active": true},
		// Private: this app is for one person's servers, and a public app is
		// one anybody can install on anything.
		"public":         false,
		"default_events": []string{"push", "workflow_job", "workflow_run"},
		"default_permissions": map[string]string{
			"contents":         "read",
			"metadata":         "read",
			"repository_hooks": "write",
			"administration":   "write",
			"actions":          "read",
		},
		"organization_permissions": map[string]string{
			"self_hosted_runners": "write",
		},
	}
}

// StartManifest remembers that this server asked for an app, and returns the
// one-time state to send with the manifest.
//
// The state is what makes the code arriving at the callback this server's code
// rather than one somebody else's browser was handed: without it, the callback
// is a URL that converts whatever code it is given into credentials this server
// then trusts.
func (c *Client) StartManifest(ctx context.Context) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b[:])
	// Stored with the moment it was made: a state that has been lying around
	// for an hour is not one somebody is in the middle of using.
	return state, c.st.SetSetting(ctx, settingManifestState,
		state+"|"+strconv.FormatInt(time.Now().Unix(), 10))
}

// ConvertManifest exchanges the code GitHub sent back for the app's
// credentials, and stores them.
func (c *Client) ConvertManifest(ctx context.Context, code, state string) (Config, error) {
	want, _, _ := c.st.Setting(ctx, settingManifestState)
	saved, at, ok := strings.Cut(want, "|")
	if !ok || saved == "" {
		return Config{}, errors.New("this server did not ask GitHub for an app; start again from Settings → GitHub")
	}
	// Constant time because it is a secret being compared, and cheap.
	if subtle.ConstantTimeCompare([]byte(saved), []byte(strings.TrimSpace(state))) != 1 {
		return Config{}, errors.New("that app was created for a different request; start again from Settings → GitHub")
	}
	if n, err := strconv.ParseInt(at, 10, 64); err == nil && time.Since(time.Unix(n, 0)) > time.Hour {
		return Config{}, errors.New("that took more than an hour; start again from Settings → GitHub")
	}
	_ = c.st.SetSetting(ctx, settingManifestState, "")

	if strings.TrimSpace(code) == "" {
		return Config{}, errors.New("GitHub sent no code back")
	}
	var out struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		ClientID      string `json:"client_id"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
	}
	// No credential on this call: the code is the credential, it is good once,
	// and it is good for ten minutes.
	if err := c.call(ctx, "", "POST", "/app-manifests/"+code+"/conversions", nil, &out); err != nil {
		return Config{}, fmt.Errorf("GitHub would not hand over the app: %w", err)
	}
	if out.ID == 0 || out.PEM == "" {
		return Config{}, errors.New("GitHub's answer had no app in it")
	}
	cfg := Config{
		AppID:         strconv.FormatInt(out.ID, 10),
		ClientID:      out.ClientID,
		PrivateKey:    out.PEM,
		WebhookSecret: out.WebhookSecret,
		Slug:          out.Slug,
	}
	if err := c.Save(ctx, cfg); err != nil {
		return Config{}, err
	}
	cfg.PrivateKey, cfg.WebhookSecret = "", ""
	cfg.Configured = true
	return cfg, nil
}

// InstallURL is where somebody goes to let the app see their repositories. An
// app that exists and is installed nowhere can see nothing, which looks exactly
// like a broken connection.
func (c *Client) InstallURL(ctx context.Context) string {
	slug := c.Load(ctx).Slug
	if slug == "" {
		return ""
	}
	return "https://github.com/apps/" + slug + "/installations/new"
}
