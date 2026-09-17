package auth

import (
	"strings"
	"testing"
)

// Each scope grants writing in its own area and nowhere else. An agent given
// "domains" to put a site behind a name must not thereby be able to take the
// firewall down, empty a backup or read a file off the disk.
func TestAScopeCoversOnlyItsOwnArea(t *testing.T) {
	writable := map[string]string{
		"deploy":     "/api/v1/apps",
		"cron":       "/api/v1/cron/jobs",
		"db":         "/api/v1/databases",
		"containers": "/api/v1/docker/containers",
		"domains":    "/api/v1/domains",
		"backups":    "/api/v1/backups/plans",
		"security":   "/api/v1/security/fix/swap",
		"uptime":     "/api/v1/uptime/checks",
		"runners":    "/api/v1/runners/pools",
		"catalog":    "/api/v1/catalog/install",
		"workspaces": "/api/v1/workspaces",
		"settings":   "/api/v1/settings/theme",
	}
	for scope, own := range writable {
		if !ScopeAllows(scope, "POST", own) {
			t.Errorf("%q should be able to POST its own area %s", scope, own)
		}
		for other, path := range writable {
			if other == scope {
				continue
			}
			if ScopeAllows(scope, "POST", path) {
				t.Errorf("%q must not be able to POST %s, which belongs to %q", scope, path, other)
			}
		}
	}
}

// Nothing short of "*" may mint a token, change an account or read one. A
// narrow scope that could widen itself would make every other rule here
// decorative.
func TestNoScopeCanWidenItself(t *testing.T) {
	paths := []string{
		"/api/v1/auth/tokens",
		"/api/v1/auth/password",
		"/api/v1/auth/totp/disable",
		"/api/v1/users",
		"/api/v1/users/abc",
	}
	for _, scope := range append(append([]string{}, Scopes...), "read,deploy,settings,security,files") {
		for _, p := range paths {
			for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
				if ScopeAllows(scope, m, p) {
					t.Errorf("scope %q must not reach %s %s", scope, m, p)
				}
			}
		}
	}
	// The one thing a token may always do is say who it belongs to.
	if !ScopeAllows("read", "GET", "/api/v1/auth/me") {
		t.Error("a token should be able to read its own identity")
	}
	// And "*" is the deliberate exception, which is why it is not in Scopes.
	if !ScopeAllows("*", "POST", "/api/v1/auth/tokens") {
		t.Error("* is meant to be able to do everything the user can")
	}
	for _, s := range Scopes {
		if s == "*" {
			t.Error(`"*" must not be offered as an ordinary scope`)
		}
	}
}

// A shell is root on the box. These routes are GETs only because that is how a
// WebSocket opens, so no amount of read access may reach them.
func TestOnlyShellOpensAShell(t *testing.T) {
	for _, p := range []string{
		"/api/v1/terminal/ws",
		"/api/v1/docker/containers/abc/exec",
		"/api/v1/workspaces/w1/attach",
		"/api/v1/workspaces/w1/agents/a1/attach",
	} {
		if ScopeAllows("read", "GET", p) {
			t.Errorf("read must not reach %s", p)
		}
		if ScopeAllows("read,workspaces,containers,system", "GET", p) {
			t.Errorf("no combination short of shell may reach %s", p)
		}
		if !ScopeAllows("shell", "GET", p) {
			t.Errorf("shell should reach %s", p)
		}
	}
}

// "read" reads everywhere and writes nowhere.
func TestReadReadsEverywhereAndWritesNothing(t *testing.T) {
	for _, a := range areas {
		p := a.prefix
		if !ScopeAllows("read", "GET", p) {
			t.Errorf("read should GET %s", p)
		}
		if ScopeAllows("read", "POST", p) {
			t.Errorf("read must not POST %s", p)
		}
	}
}

// A read token must not be able to read the disk. The file API serves any path
// the daemon can open, as root, so "read" covering it would mean every secret
// on the machine — an app's .env, the daemon's database, /etc/shadow.
func TestReadingFilesNeedsItsOwnScope(t *testing.T) {
	for _, p := range []string{"/api/v1/files", "/api/v1/files/read", "/api/v1/files/write"} {
		if ScopeAllows("read", "GET", p) {
			t.Errorf("read must not reach %s", p)
		}
		if ScopeAllows("read,deploy,domains,system,catalog", "GET", p) {
			t.Errorf("no combination short of files may reach %s", p)
		}
		if !ScopeAllows("files", "GET", p) {
			t.Errorf("files should reach %s", p)
		}
	}
	if !ScopeAllows("files", "POST", "/api/v1/files/write") {
		t.Error("files should be able to write too")
	}
}

// No token may reveal a secret. A token that could read every secret on the
// server would be a copy of the server, so revealing is left to an admin with
// a session, and written down every time.
func TestNoTokenCanRevealASecret(t *testing.T) {
	for _, sc := range append(append([]string{}, Scopes...), "*", "vault,settings,system") {
		if sc == "*" {
			continue // the deliberate exception, checked elsewhere
		}
		if ScopeAllows(sc, "POST", "/api/v1/vault/DATABASE_PASSWORD/reveal") {
			t.Errorf("scope %q must not be able to reveal a secret", sc)
		}
	}
	// Storing and listing still work, or the vault would be unusable by a
	// deploy pipeline, which is most of the point.
	if !ScopeAllows("vault", "POST", "/api/v1/vault") {
		t.Error("the vault scope should be able to store a secret")
	}
	if !ScopeAllows("read", "GET", "/api/v1/vault") {
		t.Error("read should be able to list the names")
	}
	if ScopeAllows("read", "POST", "/api/v1/vault") {
		t.Error("read must not be able to store one")
	}
}

// Notify sends. It does not read the channel list, which holds webhook URLs
// and bot tokens.
func TestNotifySendsButDoesNotRead(t *testing.T) {
	if !ScopeAllows("notify", "POST", "/api/v1/notify/emit") {
		t.Error("notify should be able to send")
	}
	if ScopeAllows("notify", "GET", "/api/v1/notify/channels") {
		t.Error("notify must not read channel configuration")
	}
	if !ScopeAllows("read", "GET", "/api/v1/notify/channels") {
		t.Error("read should still be able to list channels")
	}
}

// A path nobody has thought about is refused, rather than inheriting read
// because it happens to answer a GET.
func TestUnknownPathsAreRefused(t *testing.T) {
	for _, p := range []string{"/api/v1/something-new", "/api/v2/apps", "/internal/debug", "/"} {
		for _, s := range []string{"read", "deploy", "system", strings.Join(Scopes, ",")} {
			if ScopeAllows(s, "GET", p) {
				t.Errorf("scope %q must not reach unknown path %s", s, p)
			}
		}
	}
}

// The scopes a token can be given are the scopes the rules understand: one
// offered in the panel that grants nothing would be a lie, and a rule keyed to
// a scope nobody can be given would be dead.
func TestEveryOfferedScopeIsUnderstood(t *testing.T) {
	// The scopes handled by a case of their own rather than by the area table.
	known := map[string]bool{"read": true, "shell": true, "notify": true, "logs": true, "files": true, "vault": true}
	for _, a := range areas {
		known[a.scope] = true
	}
	for _, s := range Scopes {
		if !known[s] {
			t.Errorf("scope %q is offered but no rule uses it", s)
		}
	}
	for _, a := range areas {
		found := false
		for _, s := range Scopes {
			if s == a.scope {
				found = true
			}
		}
		if !found {
			t.Errorf("area %q needs scope %q, which is not offered", a.prefix, a.scope)
		}
	}
}

// Models and their keys: listing is a read, adding one is configuration —
// a credential goes in, the same as the GitHub App's key.
func TestAIProviderScopes(t *testing.T) {
	cases := []struct {
		method, path, scopes string
		want                 bool
	}{
		{"GET", "/api/v1/ai/providers", "read", true},
		{"POST", "/api/v1/ai/providers", "read", false},
		{"POST", "/api/v1/ai/providers", "settings", true},
		{"DELETE", "/api/v1/ai/providers/abc", "read", false},
		{"DELETE", "/api/v1/ai/providers/abc", "settings", true},
		{"GET", "/api/v1/ai/providers", "deploy", false},
	}
	for _, c := range cases {
		if got := ScopeAllows(c.scopes, c.method, c.path); got != c.want {
			t.Errorf("%s %s with %q = %v, want %v", c.method, c.path, c.scopes, got, c.want)
		}
	}
}
