package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stub captures what was sent and replies with what it is told to.
func stub(t *testing.T, reply string, got *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			if err := json.Unmarshal(b, got); err != nil {
				t.Errorf("request was not JSON: %v", err)
			}
			(*got)["__headers"] = map[string]any{
				"x-api-key":         r.Header.Get("x-api-key"),
				"anthropic-version": r.Header.Get("anthropic-version"),
				"authorization":     r.Header.Get("authorization"),
			}
			(*got)["__path"] = r.URL.Path
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
}

func TestAnthropicSendsAndParsesToolUse(t *testing.T) {
	var sent map[string]any
	srv := stub(t, `{"stop_reason":"tool_use","content":[
		{"type":"text","text":"Let me look."},
		{"type":"tool_use","id":"toolu_1","name":"list_domains","input":{"limit":5}}]}`, &sent)
	defer srv.Close()

	p := &Anthropic{Key: "sk-test", BaseURL: srv.URL, HTTP: srv.Client()}
	msg, err := p.Complete(context.Background(), "you manage a server",
		[]Message{{Role: RoleUser, Text: "what domains?"}},
		[]Tool{{Name: "list_domains", Description: "List them.", Schema: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}

	// The request.
	if sent["__path"] != "/v1/messages" {
		t.Errorf("path %v", sent["__path"])
	}
	h := sent["__headers"].(map[string]any)
	if h["x-api-key"] != "sk-test" || h["anthropic-version"] != anthropicVersion {
		t.Errorf("headers %v", h)
	}
	if sent["model"] != DefaultAnthropicModel {
		t.Errorf("model %v, want the documented default", sent["model"])
	}
	if _, ok := sent["thinking"]; ok {
		t.Error("no thinking parameter should be sent: this generation thinks adaptively and rejects a budget")
	}
	if sent["system"] != "you manage a server" {
		t.Errorf("system %v", sent["system"])
	}
	tools := sent["tools"].([]any)
	if tl := tools[0].(map[string]any); tl["name"] != "list_domains" || tl["input_schema"] == nil {
		t.Errorf("tool sent as %v; this API wants input_schema", tl)
	}

	// The reply.
	if msg.Text != "Let me look." {
		t.Errorf("text %q", msg.Text)
	}
	if len(msg.Calls) != 1 || msg.Calls[0].Name != "list_domains" || msg.Calls[0].ID != "toolu_1" {
		t.Fatalf("calls %+v", msg.Calls)
	}
	if msg.Calls[0].Input["limit"].(float64) != 5 {
		t.Errorf("arguments were not parsed: %+v", msg.Calls[0].Input)
	}
}

// A refusal is an HTTP 200 with nothing useful in content. Read naively it
// looks like the model answering with silence.
func TestAnthropicRefusalIsAnError(t *testing.T) {
	srv := stub(t, `{"stop_reason":"refusal","stop_details":{"category":"cyber","explanation":"declined"},"content":[]}`, nil)
	defer srv.Close()
	p := &Anthropic{Key: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("a refusal must surface as an error, got %v", err)
	}
}

// Tool results ride on a user turn as content blocks here.
func TestAnthropicRendersToolResults(t *testing.T) {
	var sent map[string]any
	srv := stub(t, `{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`, &sent)
	defer srv.Close()
	p := &Anthropic{Key: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := p.Complete(context.Background(), "", []Message{
		{Role: RoleUser, Text: "go"},
		{Role: RoleAssistant, Calls: []ToolCall{{ID: "t1", Name: "x", Input: map[string]any{}}}},
		{Role: RoleUser, Results: []ToolResult{{CallID: "t1", Content: "boom", IsError: true}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgs := sent["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != RoleUser {
		t.Errorf("results should be on a user turn, got %v", last["role"])
	}
	blk := last["content"].([]any)[0].(map[string]any)
	if blk["type"] != "tool_result" || blk["tool_use_id"] != "t1" || blk["is_error"] != true {
		t.Errorf("tool_result block wrong: %v", blk)
	}
}

func TestOpenAISendsAndParsesToolCalls(t *testing.T) {
	var sent map[string]any
	srv := stub(t, `{"choices":[{"message":{"content":"checking","tool_calls":[
		{"id":"call_1","type":"function","function":{"name":"list_domains","arguments":"{\"limit\":5}"}}]}}]}`, &sent)
	defer srv.Close()

	p := &OpenAI{Key: "sk-x", Model: "gpt-x", BaseURL: srv.URL, HTTP: srv.Client(), Label: "Test"}
	msg, err := p.Complete(context.Background(), "sys",
		[]Message{{Role: RoleUser, Text: "hi"}},
		[]Tool{{Name: "list_domains", Description: "d", Schema: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sent["__path"] != "/chat/completions" {
		t.Errorf("path %v", sent["__path"])
	}
	if h := sent["__headers"].(map[string]any); h["authorization"] != "Bearer sk-x" {
		t.Errorf("auth header %v", h["authorization"])
	}
	// The system prompt is a message in this shape, not a field.
	first := sent["messages"].([]any)[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("system should be the first message, got %v", first)
	}
	fn := sent["tools"].([]any)[0].(map[string]any)
	if fn["type"] != "function" || fn["function"].(map[string]any)["parameters"] == nil {
		t.Errorf("tool sent as %v; this API wants function.parameters", fn)
	}
	// Arguments arrive as a JSON string and have to be parsed.
	if len(msg.Calls) != 1 || msg.Calls[0].Input["limit"].(float64) != 5 {
		t.Fatalf("calls %+v", msg.Calls)
	}
}

// A local model behind Ollama or llama.cpp has no key, and sending an empty
// bearer header upsets some of them.
func TestOpenAISendsNoAuthHeaderWithoutAKey(t *testing.T) {
	var sent map[string]any
	srv := stub(t, `{"choices":[{"message":{"content":"hi"}}]}`, &sent)
	defer srv.Close()
	p := &OpenAI{Model: "llama", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if h := sent["__headers"].(map[string]any); h["authorization"] != "" {
		t.Errorf("no key means no authorization header, got %q", h["authorization"])
	}
}

func TestProvidersReportHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	for _, p := range []Provider{
		&Anthropic{Key: "bad", BaseURL: srv.URL, HTTP: srv.Client()},
		&OpenAI{Key: "bad", Model: "m", BaseURL: srv.URL, HTTP: srv.Client()},
	} {
		_, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "invalid x-api-key") {
			t.Errorf("%s should pass the API's own message through, got %v", p.Name(), err)
		}
	}
}

func TestAnthropicNeedsAKey(t *testing.T) {
	p := &Anthropic{}
	if _, err := p.Complete(context.Background(), "", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "no Anthropic API key") {
		t.Errorf("want a clear error, got %v", err)
	}
}

// The subscription provider shells out, so it is tested with a stand-in binary
// rather than the real one: running the real claude would spend the person's
// quota on every `go test`.
func TestSubscriptionRunsTheBinaryAndReturnsItsOutput(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat > " + filepath.Join(dir, "stdin.txt") + "\necho \"$@\" > " + filepath.Join(dir, "args.txt") + "\necho 'Two domains are configured.'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &Subscription{Bin: bin, MCPConfig: "/etc/islet/mcp.json", Model: "claude-opus-5", Dir: dir}
	msg, err := p.Complete(context.Background(), "you manage a server",
		[]Message{{Role: RoleUser, Text: "how many domains?"}}, []Tool{{Name: "ignored"}})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "Two domains are configured." {
		t.Errorf("reply %q", msg.Text)
	}
	// Claude Code runs its own loop over MCP, so the outer loop must see no
	// calls or it would try to run tools a second time.
	if len(msg.Calls) != 0 {
		t.Errorf("this provider must never return tool calls, got %+v", msg.Calls)
	}

	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	for _, want := range []string{"--print", "--mcp-config", "/etc/islet/mcp.json", "--model", "claude-opus-5"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	// The conversation goes in on stdin: an argument list has a limit a
	// transcript reaches.
	in, _ := os.ReadFile(filepath.Join(dir, "stdin.txt"))
	if !strings.Contains(string(in), "how many domains?") || !strings.Contains(string(in), "you manage a server") {
		t.Errorf("stdin did not carry the conversation: %q", in)
	}
}

// Not being signed in is the most common failure and the one whose fix is
// least obvious, so it is named rather than passed through raw.
func TestSubscriptionExplainsNotBeingSignedIn(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'Please run login first' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Subscription{Bin: bin}
	_, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("want a sign-in explanation, got %v", err)
	}
}

func TestSubscriptionSaysWhenClaudeIsMissing(t *testing.T) {
	p := &Subscription{Bin: "/nonexistent/claude-binary"}
	_, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("want a clear missing-binary error, got %v", err)
	}
}

// Print mode cannot show a permission prompt, so Claude Code refuses every MCP
// tool unless it is granted up front. Without this the assistant answers "I
// could not, permission was never granted" to everything — which reads like a
// broken tool rather than an ungranted one.
func TestSubscriptionGrantsTheMCPServersInItsConfig(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(dir, "args.txt")+"\ncat >/dev/null\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"islet":{"type":"http","url":"https://x/mcp"},"other":{"type":"http","url":"https://y/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Subscription{Bin: bin, MCPConfig: cfg}
	if _, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	got := string(args)
	if !strings.Contains(got, "--allowed-tools") {
		t.Fatalf("no grant was passed: %q", got)
	}
	for _, want := range []string{"mcp__islet", "mcp__other"} {
		if !strings.Contains(got, want) {
			t.Errorf("server %q from the config was not granted: %q", want, got)
		}
	}
	// Only the MCP servers. Granting Bash or file editing would go around the
	// token's scopes entirely, and that token is the only fence here.
	for _, never := range []string{"Bash", "Edit", "Write", "bypassPermissions"} {
		if strings.Contains(got, never) {
			t.Errorf("%q must never be granted: %q", never, got)
		}
	}
}

// A configuration that cannot be read grants nothing rather than guessing a
// server name, so it fails closed.
func TestSubscriptionGrantsNothingWithoutAReadableConfig(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(dir, "args.txt")+"\ncat >/dev/null\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Subscription{Bin: bin, MCPConfig: filepath.Join(dir, "does-not-exist.json")}
	if _, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if strings.Contains(string(args), "--allowed-tools") {
		t.Errorf("nothing should be granted from an unreadable config: %q", args)
	}
}
