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
	"time"
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
	script := "#!/bin/sh\ncat > " + filepath.Join(dir, "stdin.txt") + "\necho \"$@\" > " + filepath.Join(dir, "args.txt") + "\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"Two domains are configured.\"}'\n"
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
	for _, want := range []string{"--print", "--output-format stream-json", "--verbose", "--mcp-config", "/etc/islet/mcp.json", "--model", "claude-opus-5"} {
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
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(dir, "args.txt")+"\ncat >/dev/null\necho '{\"type\":\"result\",\"result\":\"ok\"}'\n"), 0o755); err != nil {
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
	//
	// The check is on what --allowed-tools carries rather than on the whole
	// command line, because those names now appear in the deny list too — which
	// is the opposite of granting them, and the point of the next test.
	grant := ""
	if i := strings.Index(got, "--allowed-tools "); i >= 0 {
		grant = strings.TrimSpace(got[i+len("--allowed-tools "):])
	}
	if grant == "" {
		t.Fatalf("no grant was passed: %q", got)
	}
	for _, never := range []string{"Bash", "Edit", "Write", "bypassPermissions"} {
		if strings.Contains(grant, never) {
			t.Errorf("%q must never be granted: %q", never, grant)
		}
	}
	if strings.Contains(got, "--dangerously-skip-permissions") || strings.Contains(got, "bypassPermissions") {
		t.Errorf("permissions must never be bypassed: %q", got)
	}
}

// A configuration that cannot be read grants nothing rather than guessing a
// server name, so it fails closed.
func TestSubscriptionGrantsNothingWithoutAReadableConfig(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(dir, "args.txt")+"\ncat >/dev/null\necho '{\"type\":\"result\",\"result\":\"ok\"}'\n"), 0o755); err != nil {
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

// Claude Code runs its own tool loop, so without reading its event stream a run
// that made nine calls reaches the panel as one turn several minutes later with
// nothing in between — and on a phone, several minutes of nothing looks exactly
// like a hang. These are the shapes the real binary emits, taken from a
// recorded run of `claude --print --output-format stream-json --verbose`.
func TestSubscriptionReportsEachToolAsItIsUsed(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"I will look at the domains."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__islet__list_domains","input":{"limit":10}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"[{\"host\":\"a.example.com\"}]"}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"mcp__islet__create_domain","input":{"host":"b.example.com"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"scopes do not cover domains"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"One domain, and I could not add the second."}`,
	}
	script := "#!/bin/sh\ncat >/dev/null\n"
	for _, l := range lines {
		script += "echo '" + l + "'\n"
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var events []string
	obs := &Observer{
		Text:      func(s string) { events = append(events, "text:"+s) },
		ToolStart: func(c ToolCall) { events = append(events, "start:"+c.Name) },
		ToolEnd: func(c ToolCall, r ToolResult, d time.Duration) {
			state := "ok"
			if r.IsError {
				state = "failed"
			}
			events = append(events, "end:"+c.Name+":"+state)
		},
	}
	p := &Subscription{Bin: bin}
	msg, err := p.CompleteStream(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil, obs)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "One domain, and I could not add the second." {
		t.Errorf("answer %q", msg.Text)
	}
	want := []string{
		"text:I will look at the domains.",
		// The MCP prefix is dropped: nobody watching needs to read
		// mcp__islet__list_domains to know what is happening.
		"start:list_domains",
		"end:list_domains:ok",
		"start:create_domain",
		// A refused tool is reported as refused, not hidden. It is the most
		// useful thing on the screen when it happens.
		"end:create_domain:failed",
	}
	if len(events) != len(want) {
		t.Fatalf("events:\n%v\nwant:\n%v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, events[i], want[i])
		}
	}
}

// A failed run says why. The binary reports its own failures in the result
// line rather than on stderr, so an exit code of zero can still be a failure.
func TestSubscriptionReportsAFailureFromTheResultLine(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat >/dev/null\necho '{\"type\":\"result\",\"subtype\":\"error_max_turns\",\"is_error\":true,\"result\":\"reached the turn limit\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Subscription{Bin: bin}
	_, err := p.CompleteStream(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "turn limit") {
		t.Fatalf("want the failure from the result line, got %v", err)
	}
}

// A line this does not understand — a new event type, a partial-message chunk —
// must not end the parse. The answer is still in the stream behind it.
func TestSubscriptionIgnoresEventsItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"echo '{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_delta\"}}'\n" +
		"echo 'not json at all'\n" +
		"echo '{\"type\":\"rate_limit_event\"}'\n" +
		"echo '{\"type\":\"result\",\"result\":\"done anyway\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Subscription{Bin: bin}
	msg, err := p.CompleteStream(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "done anyway" {
		t.Errorf("answer %q", msg.Text)
	}
}

// The assistant's authority is the token in its MCP configuration and the
// scopes on it. Claude Code's own tools go round all of it: the daemon runs as
// root, so Bash is a root shell and Read is every file on the machine, neither
// scoped nor written to the audit log.
//
// This was not theoretical. With only --allowed-tools, which is what shipped,
// the assistant was asked to run `id -u` and answered 0.
func TestSubscriptionRefusesClaudeCodesOwnTools(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(dir, "args.txt")+"\ncat >/dev/null\necho '{\"type\":\"result\",\"result\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Subscription{Bin: bin, MCPConfig: filepath.Join(dir, "mcp.json")}
	if err := os.WriteFile(p.MCPConfig, []byte(`{"mcpServers":{"islet":{"type":"http","url":"https://x/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	got := string(args)

	// Nobody can answer a prompt, so anything that would ask is refused rather
	// than waiting for a terminal that is not there.
	if !strings.Contains(got, "--permission-prompts none") {
		t.Errorf("nothing refuses what would prompt: %q", got)
	}
	// Only the servers in the file Islet wrote. Otherwise an MCP server
	// configured for whoever runs the daemon is loaded too, and the assistant
	// has tools nobody granted it.
	if !strings.Contains(got, "--strict-mcp-config") {
		t.Errorf("another MCP configuration could be loaded: %q", got)
	}
	// And the tools that reach the machine directly are denied by name.
	for _, tool := range []string{"Bash", "Read", "Write", "Edit", "WebFetch", "Task"} {
		if !strings.Contains(got, `"`+tool+`"`) {
			t.Errorf("%s is not denied: %q", tool, got)
		}
	}
	if !strings.Contains(got, "manual") {
		t.Errorf("the default is not to ask: %q", got)
	}
	// The MCP server is still granted, or the assistant can do nothing at all.
	if !strings.Contains(got, "mcp__islet") {
		t.Errorf("the tools it is supposed to have were not granted: %q", got)
	}
}

// The policy is built from a literal, so it cannot silently become permissive.
func TestTheDenyListIsNeverEmpty(t *testing.T) {
	s := claudeSettings()
	for _, tool := range deniedTools {
		if !strings.Contains(s, `"`+tool+`"`) {
			t.Errorf("%s is missing from the settings the binary is given: %s", tool, s)
		}
	}
	if len(deniedTools) < 10 {
		t.Errorf("the deny list has shrunk to %d entries, which is how a shell gets back in", len(deniedTools))
	}
}

// A conversation read back tomorrow, on another device, should still say what
// the assistant did — not just what it concluded. The subscription provider
// never returns tool calls, because Claude Code ran them itself, so the record
// travels on the turn instead.
func TestTheTurnKeepsWhatItDid(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__islet__list_domains","input":{"limit":5}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"[{\"host\":\"a.example.com\"}]"}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"mcp__islet__create_domain","input":{"host":"b.example.com"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"scopes do not cover domains"}]}}`,
		`{"type":"result","result":"One domain; I could not add the other."}`,
	}
	script := "#!/bin/sh\ncat >/dev/null\n"
	for _, l := range lines {
		script += "echo '" + l + "'\n"
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := (&Subscription{Bin: bin}).Complete(context.Background(), "", []Message{{Role: RoleUser, Text: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Tools) != 2 {
		t.Fatalf("the turn kept %d tool runs, want 2: %+v", len(msg.Tools), msg.Tools)
	}
	if msg.Tools[0].Name != "list_domains" || !msg.Tools[0].OK {
		t.Errorf("first run recorded as %+v", msg.Tools[0])
	}
	if msg.Tools[1].Name != "create_domain" || msg.Tools[1].OK {
		t.Errorf("the refusal was not recorded as one: %+v", msg.Tools[1])
	}
	if msg.Tools[1].Output != "scopes do not cover domains" {
		t.Errorf("the refusal lost its reason: %q", msg.Tools[1].Output)
	}
	// And it is a record, not an instruction: the outer loop must never see
	// these as calls to make, or every reopened conversation would run again.
	if len(msg.Calls) != 0 {
		t.Errorf("this provider must never return tool calls: %+v", msg.Calls)
	}
}
