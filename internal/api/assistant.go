package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/assistant"
	"github.com/isletdev/islet/pkg/api"
)

// assistantSystem is what the model is told it is. It is deliberately short:
// the tools describe themselves, and a long preamble mostly displaces the
// conversation that matters.
const assistantSystem = `You operate one Linux server through Islet, a control panel.

Use the tools to find out what is true before you act; several read-only tools
exist for exactly that. When you change something, say plainly what you changed.

You are acting as a real person on their real server. Prefer the smallest change
that does what was asked, and prefer asking over guessing when a request could
mean two things — particularly when it is destructive.

Some tools will be refused: the token you are acting under is scoped, and the
refusal names the scope that would be needed. Do not try to work around it; say
which scope is missing so the person can decide whether to grant it.`

// assistantHeartbeat is how often an idle stream sends a line. It has to be
// comfortably under the shortest idle timeout in front of the panel —
// Cloudflare's origin timeout is 100 seconds — because a single tool call can
// be a build that takes minutes, and during it nothing else has anything to say.
var assistantHeartbeat = 20 * time.Second

// assistantTools presents the MCP tool set to a model. It is the same set the
// agent over JSON-RPC sees — one definition, so the panel's assistant and an
// external agent cannot end up with different ideas of what this server can do.
func (s *Server) assistantTools() []assistant.Tool {
	if s.mcp == nil {
		return nil
	}
	tools := s.mcp.Tools()
	out := make([]assistant.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, assistant.Tool{Name: t.Name, Description: t.Description, Schema: t.InputSchema})
	}
	return out
}

// assistantExecutor runs a tool as the person who asked, narrowed by the scopes
// they are acting under.
//
// The assistant gets no authority of its own. It runs through mcp.CallTool,
// which is the same gate the JSON-RPC endpoint passes, so a question typed into
// the panel can do exactly what the person asking could do through the API and
// nothing more — and the refusal, when there is one, says which scope was
// missing rather than failing silently.
func (s *Server) assistantExecutor(ctx context.Context, actor, scopes, role string) assistant.Executor {
	return func(ctx context.Context, c assistant.ToolCall) (string, error) {
		if s.mcp == nil {
			return "", fmt.Errorf("no tools are available")
		}
		out, err := s.mcp.CallTool(ctx, actor, scopes, role, c.Name, c.Input)
		if err != nil {
			// Returned as an error so the loop reports it to the model as a
			// failed result: it is information the model should act on, not a
			// reason to stop.
			return "", err
		}
		// A very long tool result is worse than a truncated one: it crowds out
		// the conversation and costs the person money to send back on every
		// later turn.
		const max = 24 << 10
		if len(out) > max {
			return out[:max] + fmt.Sprintf("\n… truncated, %d bytes in total", len(out)), nil
		}
		return out, nil
	}
}

// scopesFor works out what the caller may do. A session in the panel acts with
// the person's full authority; a request carrying a token is narrowed to that
// token's scopes, which is what makes "ask the assistant" no more powerful than
// "call the API yourself".
func scopesForRequest(ctx context.Context) (actor, scopes, role string) {
	u := userFrom(ctx)
	if u == nil {
		return "", "", ""
	}
	scopes = "*"
	if t := tokenFrom(ctx); t != nil {
		scopes = t.Scopes
	}
	return "assistant:" + u.Username, scopes, u.Role
}

// secret reads an encrypted setting, and setSecret writes one. The same shape
// as the registry credentials and the shared env groups: AES-GCM through the
// daemon's keys, base64 in the settings table. An API key is a credential like
// any other and is stored like the others rather than in a scheme of its own.
func (s *Server) secret(ctx context.Context, key string) (string, error) {
	v, _, err := s.store.Setting(ctx, key)
	if err != nil || v == "" {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return "", err
	}
	p, err := s.keys.Decrypt(b)
	if err != nil {
		return "", err
	}
	return string(p), nil
}

func (s *Server) setSecret(ctx context.Context, key, value string) error {
	if value == "" {
		return s.store.SetSetting(ctx, key, "")
	}
	enc, err := s.keys.Encrypt([]byte(value))
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, key, base64.StdEncoding.EncodeToString(enc))
}

// providerFor builds the provider from stored settings.
func (s *Server) providerFor(ctx context.Context) (assistant.Provider, error) {
	kind, _, _ := s.store.Setting(ctx, "assistant.provider")
	model, _, _ := s.store.Setting(ctx, "assistant.model")
	baseURL, _, _ := s.store.Setting(ctx, "assistant.base_url")
	key, err := s.secret(ctx, "assistant.key")
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(kind) {
	case "", "anthropic":
		return &assistant.Anthropic{Key: key, Model: model, BaseURL: baseURL}, nil
	case "openai":
		return &assistant.OpenAI{Key: key, Model: model, BaseURL: baseURL, Label: "OpenAI-compatible"}, nil
	case "subscription":
		// No key: this one spends the subscription the person signed into on
		// this server. The MCP configuration is what gives it Islet's tools,
		// and the token inside it is what bounds them.
		bin, _, _ := s.store.Setting(ctx, "assistant.claude_bin")
		if bin == "" && s.workspaces != nil {
			bin = s.workspaces.ClaudePath(ctx)
		}
		cfg, _, _ := s.store.Setting(ctx, "assistant.mcp_config")
		return &assistant.Subscription{Bin: bin, MCPConfig: cfg, Model: model}, nil
	default:
		return nil, fmt.Errorf("unknown assistant provider %q", kind)
	}
}

// handleAssistantConfig reads and writes which provider to use. The key is
// never sent back — only whether one is set, which is what the panel needs to
// draw the difference between "not configured" and "configured".
func (s *Server) handleAssistantConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		kind, _, _ := s.store.Setting(r.Context(), "assistant.provider")
		model, _, _ := s.store.Setting(r.Context(), "assistant.model")
		base, _, _ := s.store.Setting(r.Context(), "assistant.base_url")
		key, _ := s.secret(r.Context(), "assistant.key")
		if kind == "" {
			kind = "anthropic"
		}
		mcpCfg, _, _ := s.store.Setting(r.Context(), "assistant.mcp_config")
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": kind, "model": model, "baseUrl": base,
			"keySet":       key != "",
			"defaultModel": assistant.DefaultAnthropicModel,
			"tools":        len(s.assistantTools()),
			"mcpConfig":    mcpCfg,
			// Whether the subscription route is even possible here.
			"claudeInstalled": s.workspaces != nil && s.workspaces.ClaudePath(r.Context()) != "",
		})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		BaseURL   string `json:"baseUrl"`
		Key       string `json:"key"`
		MCPConfig string `json:"mcpConfig"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	switch req.Provider {
	case "anthropic", "openai", "subscription":
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "provider must be anthropic or openai"})
		return
	}
	for k, v := range map[string]string{
		"assistant.provider":   req.Provider,
		"assistant.model":      strings.TrimSpace(req.Model),
		"assistant.base_url":   strings.TrimSpace(req.BaseURL),
		"assistant.mcp_config": strings.TrimSpace(req.MCPConfig),
	} {
		if err := s.store.SetSetting(r.Context(), k, v); err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
	}
	// An empty key leaves the stored one alone, so saving the model does not
	// silently wipe the credential the panel never showed you.
	if strings.TrimSpace(req.Key) != "" {
		if err := s.setSecret(r.Context(), "assistant.key", strings.TrimSpace(req.Key)); err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "assistant.configure", req.Provider, req.Model)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAssistantChat answers one question, running tools as it goes.
func (s *Server) handleAssistantChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []assistant.Message `json:"messages"`
		MaxSteps int                 `json:"maxSteps"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "send at least one message"})
		return
	}
	p, err := s.providerFor(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "not_configured", Message: err.Error()})
		return
	}
	actor, scopes, role := scopesForRequest(r.Context())
	exec := s.assistantExecutor(r.Context(), actor, scopes, role)

	if !streamAssistantRun(r.Context(), w, p, req.Messages, s.assistantTools(), exec, req.MaxSteps) {
		// The response writer cannot flush, so nothing would reach the client
		// until the end anyway. Answer the old way rather than pretend.
		out, err := assistant.Run(r.Context(), p, assistantSystem, req.Messages, s.assistantTools(), exec, req.MaxSteps)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "provider", "message": err.Error(), "messages": out})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": out, "reply": assistant.Text(out)})
	}
}

// streamAssistantRun runs the loop and writes it out as newline-delimited JSON,
// one object per line. It reports false, having written nothing, when the
// response writer cannot flush.
//
// Streaming is not for elegance: a proxy in front of the panel gives the origin
// a fixed time to respond — Cloudflare's is 100 seconds — and a conversation
// that deploys something takes several minutes. Waiting for the whole loop and
// then replying means a 524 on exactly the requests worth making. Bytes moving
// keep the connection alive, and they happen to be the progress somebody
// waiting wants to see anyway.
func streamAssistantRun(ctx context.Context, w http.ResponseWriter, p assistant.Provider, msgs []assistant.Message, tools []assistant.Tool, exec assistant.Executor, maxSteps int) bool {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	// Nothing between here and the client may buffer this, or the point is
	// lost and the proxy times out with the whole reply sitting in a buffer.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	send := func(v any) {
		_ = enc.Encode(v)
		flusher.Flush()
	}
	// A first line before any model call, so the connection is established and
	// the client knows it was heard even while the first turn is being thought
	// about.
	send(map[string]any{"type": "start", "tools": len(tools)})

	// The loop runs elsewhere and reports through a channel, so that every
	// write to the response happens on this goroutine and a heartbeat can share
	// it. A turn is not enough on its own: one tool call can be a deploy that
	// builds for five minutes, and no bytes move for the whole of it.
	events := make(chan any, 8)
	post := func(v any) {
		select {
		case events <- v:
		case <-ctx.Done():
		}
	}
	go func() {
		defer close(events)
		out, err := assistant.RunStream(ctx, p, assistantSystem, msgs, tools, exec, maxSteps,
			func(m assistant.Message) { post(map[string]any{"type": "turn", "message": m}) })
		if err != nil {
			// The status is already 200 by now, so the failure travels in the
			// stream rather than in a code. The transcript so far goes with it:
			// a run that failed on the third tool call is more useful read than
			// discarded.
			post(map[string]any{"type": "error", "message": err.Error(), "messages": out})
			return
		}
		post(map[string]any{"type": "done", "messages": out, "reply": assistant.Text(out)})
	}()

	tick := time.NewTicker(assistantHeartbeat)
	defer tick.Stop()
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return true
			}
			send(e)
		case <-tick.C:
			send(map[string]any{"type": "ping"})
		case <-ctx.Done():
			return true
		}
	}
}
