package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/assistant"
	"github.com/isletdev/islet/internal/auth"
)

// Giving the subscription assistant Islet's tools.
//
// A provider that returns tool calls — Anthropic, OpenAI — is handed the tool
// list in the request and its calls come back through assistantExecutor, which
// checks each one against the asker's scopes. Claude Code does not work that
// way. It runs as a process and calls tools over HTTP for itself, so the only
// thing it can be given is an MCP configuration: a URL and a bearer token.
//
// That configuration was a field on the provider for somebody to fill in by
// hand, and nobody ever did. The effect was worse than "no tools": without
// --mcp-config there is no --strict-mcp-config either, so Claude Code fell back
// to whatever MCP servers the root account happened to have configured. On this
// machine that meant the assistant answered "I don't have the tools for that"
// while listing three servers nobody had granted it. It is written here now,
// per run, and taken away afterwards.

// assistantScopes is what the assistant may do, as a token.
//
// Deliberately not "*". The token belongs to whoever asked, so every check it
// meets was already satisfied by them — which means the list is not a wall
// against the person, it is a limit on what the model can do on their behalf
// without being asked twice. Four are held back for that reason:
//
//	shell     a root shell, which is the whole server
//	security  the firewall, which is how the server stays reachable
//	vault     the secrets, which the assistant has no reason to read out
//	cron      a job is a root command on a timer, which is a shell by post
//
// Everything else is the work: deploy something, give it a database, put it on
// a domain, look at why it is failing, back it up, say what happened.
const assistantScopes = "read,deploy,containers,domains,db,files,backups,uptime,runners,catalog,notify,logs,system,media"

// assistantTokenTTL bounds a token that escapes. A run has a thirty-minute
// ceiling; this is that with room for the revoke to be the thing that ends it.
const assistantTokenTTL = 45 * time.Minute

// toolsFor gives a provider what it needs to reach this daemon, and returns the
// cleanup for when the run is over.
//
// Only the subscription kind needs anything: it is the only one that calls
// tools by itself. Everything else already has them.
func (s *Server) toolsFor(r *http.Request, p assistant.Provider, runID string) (func(), error) {
	sub, ok := p.(*assistant.Subscription)
	if !ok {
		return func() {}, nil
	}
	// An operator who wrote their own configuration keeps it.
	if strings.TrimSpace(sub.MCPConfig) != "" {
		return func() {}, nil
	}
	if s.auth == nil || s.mcp == nil {
		return func() {}, nil
	}
	u := userFrom(r.Context())
	if u == nil {
		return func() {}, nil
	}
	// A token narrowed twice: to this list, and by the role of whoever asked.
	// A viewer asking a question gets a token that can still only read.
	secret, tok, err := s.auth.CreateToken(r.Context(), u.ID, assistantName(runID), assistantScopes, assistantTokenTTL)
	if err != nil {
		return func() {}, fmt.Errorf("the assistant could not be given its tools: %w", err)
	}
	s.markAssistantToken(tok.ID, true)
	path, err := s.writeAssistantMCP(runID, s.publicURL(r), secret)
	if err != nil {
		s.markAssistantToken(tok.ID, false)
		_ = s.auth.RevokeToken(context.WithoutCancel(r.Context()), u.ID, tok.ID, true)
		return func() {}, err
	}
	sub.MCPConfig = path

	// Both halves go together: the file is the token written down, so removing
	// one without the other leaves either a credential on disk or a file that
	// authenticates as nothing.
	done := context.WithoutCancel(r.Context())
	return func() {
		_ = os.Remove(path)
		s.markAssistantToken(tok.ID, false)
		_ = s.auth.RevokeToken(done, u.ID, tok.ID, true)
	}, nil
}

// isAssistantToken reports whether this token is one minted for a run that is
// happening now. Held in memory beside the runs themselves: it is true for a
// few minutes and false forever afterwards, which is exactly as long as the
// question it belongs to.
func (s *Server) isAssistantToken(tok *auth.Token) bool {
	if tok == nil {
		return false
	}
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	return s.assistantTokens[tok.ID]
}

func (s *Server) markAssistantToken(id string, live bool) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	if s.assistantTokens == nil {
		s.assistantTokens = map[string]bool{}
	}
	if live {
		s.assistantTokens[id] = true
		return
	}
	delete(s.assistantTokens, id)
}

func assistantName(runID string) string {
	if len(runID) > 8 {
		runID = runID[:8]
	}
	return "assistant " + runID
}

// writeAssistantMCP puts the configuration where only this daemon can read it.
//
// Under the data directory rather than anywhere Claude Code might pick up on
// its own, 0600, and named after the run so two questions at once do not share
// a file that one of them is about to delete.
func (s *Server) writeAssistantMCP(runID, url, token string) (string, error) {
	dir := filepath.Join(s.dataDir, "assistant")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "mcp-"+runID+".json")
	body := fmt.Sprintf(
		`{"mcpServers":{"islet":{"type":"http","url":%q,"headers":{"Authorization":"Bearer %s"}}}}`+"\n",
		strings.TrimRight(url, "/")+"/mcp", token)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// sweepAssistantMCP removes configurations left behind by a daemon that stopped
// mid-run. The token in each has an expiry of its own, so this is tidiness
// rather than the security of it.
func (s *Server) sweepAssistantMCP() {
	dir := filepath.Join(s.dataDir, "assistant")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "mcp-") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < assistantTokenTTL {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
