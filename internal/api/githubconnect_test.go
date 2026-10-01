package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/github"
)

// The connection is readable by anything with "read", so what it answers with
// must never include the credential itself. Blanked in the handler rather than
// in the type, so this is the test that keeps it blanked.
func TestTheGitHubConfigNeverCarriesItsSecrets(t *testing.T) {
	cfg := github.Config{
		AppID: "123", ClientID: "Iv1.abc", Slug: "islet-box",
		PrivateKey:    "-----BEGIN RSA PRIVATE KEY-----\nSECRETKEYMATERIAL\n-----END RSA PRIVATE KEY-----",
		WebhookSecret: "SECRETHOOKVALUE",
		Configured:    true,
	}
	// Exactly what handleGitHubConfig does before it writes the response.
	cfg.PrivateKey, cfg.WebhookSecret = "", ""
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRETKEYMATERIAL", "SECRETHOOKVALUE", "PRIVATE KEY"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("the config carries %q", secret)
		}
	}
	// And the handler is still the thing that does it.
	src := readSource(t, "github.go")
	if !strings.Contains(src, "cfg.PrivateKey, cfg.WebhookSecret = \"\", \"\"") {
		t.Error("handleGitHubConfig no longer blanks the credential before answering")
	}
}

// Publishing writes a .gitignore when there is none, and adds the one line that
// matters to one that exists without it. The line is .env, and the reason is
// that "publish this directory" otherwise puts an application's secrets on
// GitHub because somebody was running it locally.
func TestPublishingAlwaysIgnoresDotEnv(t *testing.T) {
	if !strings.Contains(github.DefaultIgnore(), ".env") {
		t.Error("the default .gitignore does not ignore .env")
	}
	src := readSource(t, "../github/publish.go")
	if !strings.Contains(src, "ensureIgnore") {
		t.Fatal("publishing no longer touches .gitignore at all")
	}
	// It must run before anything is staged, or the first commit has already
	// taken the file it was meant to keep out.
	pub := src[strings.Index(src, "func (c *Client) Publish("):]
	ignore := strings.Index(pub, "ensureIgnore")
	add := strings.Index(pub, `"add", "-A"`)
	if ignore < 0 || add < 0 || ignore > add {
		t.Error("the .gitignore is written after the files are staged, which is too late")
	}
}
