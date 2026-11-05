package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/internal/workspace"
)

// A workspace agent runs as root in a shell on the host, so a token narrower
// than that only stops it doing through Islet what it can do anyway. Its token
// covers every scope there is — and still never reaches the routes that mint
// tokens, which no scope does.
func TestWorkspaceTokensCoverEveryScope(t *testing.T) {
	got := map[string]bool{}
	for _, sc := range strings.Split(workspaceScopes(), ",") {
		got[sc] = true
	}
	for _, sc := range auth.Scopes {
		if !got[sc] {
			t.Errorf("a workspace token lacks %q", sc)
		}
	}
	if auth.ScopeAllows(workspaceScopes(), "POST", "/api/v1/auth/tokens") {
		t.Error("a workspace token can mint another token")
	}
	for _, need := range [][2]string{{"POST", "/api/v1/domains"}, {"POST", "/api/v1/apps"}, {"POST", "/api/v1/cron/jobs"}, {"POST", "/api/v1/databases"}} {
		if !auth.ScopeAllows(workspaceScopes(), need[0], need[1]) {
			t.Errorf("a workspace token cannot %s %s", need[0], need[1])
		}
	}
}

// Workspaces made before the change hold the old four-scope token. They are
// widened in place at start, so the token their agent already has keeps
// working and can do more on its next call.
func TestOldWorkspaceTokensAreWidenedInPlace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	keys, err := auth.LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	as, err := auth.New(st, keys, dir)
	if err != nil {
		t.Fatal(err)
	}
	u, err := as.CreateUser(ctx, "admin", "correct horse battery staple", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret, tok, err := as.CreateToken(ctx, u.ID, "workspace app", "read,logs,containers,notify", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Written straight to the table: saving through the service asks tmux
	// about the session, and this is about the token, not the session.
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO workspaces (id, server_id, name, directory, preset, command, mcp_enabled, skip_permissions, mcp_token_id) VALUES ('w1', ?, 'app', ?, 'shell', '', 1, 0, ?)`, st.ServerID, dir, tok.ID); err != nil {
		t.Fatal(err)
	}
	ws := workspace.New(st, nil, nil, dir, nil)
	(&Server{auth: as, workspaces: ws}).widenWorkspaceTokens(ctx)

	_, after, err := as.UserByToken(ctx, secret)
	if err != nil {
		t.Fatalf("the agent's token stopped working: %v", err)
	}
	if after.Scopes != workspaceScopes() {
		t.Fatalf("scopes after start = %q", after.Scopes)
	}
	// What the workspace page shows is the token's, not a list written down.
	if got := strings.Join((&Server{auth: as, workspaces: ws}).workspaceTokenScopes(httptest.NewRequest("GET", "/", nil), "w1"), ","); got != workspaceScopes() {
		t.Fatalf("the workspace reports %q", got)
	}
	if !auth.ScopeAllows(after.Scopes, "POST", "/api/v1/domains") {
		t.Error("the widened token still cannot put an app on a domain")
	}
}
