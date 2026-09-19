package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
)

// The configuration that gives the assistant its tools.
//
// This file is the whole of what Claude Code is told about this server, so its
// shape is load-bearing: a URL it can reach and a token it may use. It was a
// field for somebody to fill in by hand and was therefore empty on every
// server, which is how the panel came to say "70 tools" while the assistant
// said it had none.
func TestTheAssistantsMCPConfigIsWhatClaudeCodeNeeds(t *testing.T) {
	s := &Server{dataDir: t.TempDir()}
	path, err := s.writeAssistantMCP("run123", "https://panel.example.com/", "islet_secret")
	if err != nil {
		t.Fatal(err)
	}

	// Readable by this daemon and nobody else: it is a bearer token written
	// down.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the file holding a token is mode %o", mode)
	}
	if dir := filepath.Dir(path); filepath.Base(dir) != "assistant" {
		t.Errorf("written outside the assistant's own directory: %s", path)
	}
	// Named after the run, so two questions at once do not share a file that
	// one of them is about to delete.
	if !strings.Contains(filepath.Base(path), "run123") {
		t.Errorf("not named after the run: %s", path)
	}

	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Claude Code would not parse this: %v\n%s", err, raw)
	}
	islet, ok := cfg.MCPServers["islet"]
	if !ok {
		t.Fatalf("no islet server in the configuration: %s", raw)
	}
	if islet.Type != "http" {
		t.Errorf("type is %q", islet.Type)
	}
	// One slash, not two: the base may or may not have a trailing one.
	if islet.URL != "https://panel.example.com/mcp" {
		t.Errorf("url is %q", islet.URL)
	}
	if islet.Headers["Authorization"] != "Bearer islet_secret" {
		t.Errorf("authorization is %q", islet.Headers["Authorization"])
	}
}

// A token is only the assistant's while its run is happening. Afterwards it is
// an ordinary token again — and an ordinary token does not get past a switched
// off MCP endpoint.
func TestOnlyALiveRunsTokenSkipsTheMCPSwitch(t *testing.T) {
	s := &Server{}
	if s.isAssistantToken(nil) {
		t.Error("no token at all was treated as the assistant's")
	}
	tok := &auth.Token{ID: "t1"}
	if s.isAssistantToken(tok) {
		t.Error("an unknown token was treated as the assistant's")
	}
	s.markAssistantToken("t1", true)
	if !s.isAssistantToken(tok) {
		t.Error("the run's own token was not recognised")
	}
	s.markAssistantToken("t1", false)
	if s.isAssistantToken(tok) {
		t.Error("the token still counted after the run ended")
	}
}

// What the assistant may do as a token, written down so that widening it is a
// decision somebody makes rather than a line that drifts.
//
// The four that are absent are absent on purpose: shell is the whole server,
// security is how it stays reachable, vault is the secrets, and cron is a root
// command on a timer — a shell by post.
func TestTheAssistantsScopesHoldBackTheDangerousFour(t *testing.T) {
	have := map[string]bool{}
	for _, sc := range strings.Split(assistantScopes, ",") {
		have[strings.TrimSpace(sc)] = true
	}
	for _, must := range []string{"read", "deploy", "containers", "domains", "db", "logs"} {
		if !have[must] {
			t.Errorf("the assistant cannot %s, which is most of what it is for", must)
		}
	}
	for _, never := range []string{"shell", "security", "vault", "cron"} {
		if have[never] {
			t.Errorf("the assistant's token carries %q; see the comment on assistantScopes", never)
		}
	}
}

// The run has to hand the tools back. A file holding a bearer token that
// outlives the question it was written for is a credential nobody is watching.
func TestTheRunReleasesTheToolsItWasGiven(t *testing.T) {
	src, err := os.ReadFile("assistant.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	h := body[strings.Index(body, "func (s *Server) handleAssistantChat("):]
	h = h[:strings.Index(h, "\nfunc ")]
	if !strings.Contains(h, "s.toolsFor(") {
		t.Fatal("the run no longer asks for its tools")
	}
	if !strings.Contains(h, "defer release()") {
		t.Error("the run no longer gives its tools back when it ends")
	}
}
