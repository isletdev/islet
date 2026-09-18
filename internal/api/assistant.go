package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

// assistantFiles is added when this server can take uploads. It says where
// they are, because "the logo I sent you earlier" is a reference to a turn
// further up that the model can only resolve if it knows attachments are files
// on disk rather than something it was shown.
const assistantFiles = `

Files attached to a message are already on this server, at the paths given with
them. Read them, copy them, unpack them — they are ordinary files. They all live
under %s, so a file from earlier in this conversation is still there. Copy what
you need into the place it belongs rather than working out of that directory:
it is a drop box, not part of anybody's project.`

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

// seal and unseal are secret and setSecret without the settings table, for
// credentials that live in a row of their own. Same encryption, same base64.
func (s *Server) seal(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	enc, err := s.keys.Encrypt([]byte(value))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func (s *Server) unseal(v string) (string, error) {
	if v == "" {
		return "", nil
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

// pickProvider is providerFor plus the id it settled on, which is what a new
// conversation records. Storing the resolved id rather than the empty one means
// changing the server's default later does not move a conversation that is
// already under way.
func (s *Server) pickProvider(ctx context.Context, id string) (assistant.Provider, string, error) {
	if s.ai != nil {
		p, err := s.ai.Resolve(ctx, id)
		if err != nil {
			return nil, "", err
		}
		if p != nil {
			built, err := s.build(ctx, p)
			return built, p.ID, err
		}
	}
	built, err := s.legacyProvider(ctx)
	return built, "", err
}

// providerFor builds the model a conversation is having.
//
// The id names one; empty means the server's default. The settings-based
// configuration underneath is the fallback for a server that has no provider
// rows at all — which after the migration means one that never configured an
// assistant, and which is left in place so that a half-applied upgrade is a
// working assistant rather than a broken one.
func (s *Server) providerFor(ctx context.Context, providerID string) (assistant.Provider, error) {
	if s.ai != nil {
		p, err := s.ai.Resolve(ctx, providerID)
		if err != nil {
			return nil, err
		}
		if p != nil {
			return s.build(ctx, p)
		}
	}
	return s.legacyProvider(ctx)
}

// legacyProvider builds from the settings the assistant used before providers
// were rows.
func (s *Server) legacyProvider(ctx context.Context) (assistant.Provider, error) {
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
			// Whether the subscription route is even possible here, and
			// whether it has actually been signed in to — two different
			// questions that the panel used to ask as one, so a fresh server
			// with the binary installed reported itself ready and then
			// answered nothing.
			"claudeInstalled": s.workspaces != nil && s.workspaces.ClaudePath(r.Context()) != "",
			"claudeSignedIn":  s.workspaces != nil && s.workspaces.ClaudeSignedIn(),
			"claudePath":      claudePath(r.Context(), s),
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
		s.badJSON(w, err)
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

// handleAssistantChat starts a run and streams it.
//
// The run does not belong to this request. A phone that locks its screen, an
// app sent to the background or a tab swiped away all drop the connection, and
// while the loop was bound to the request that meant the work stopped — halfway
// through a deploy, with nothing kept. Now the connection closing ends only the
// streaming; the run carries on, and /api/v1/assistant/runs/{id} picks it up
// again from wherever the client got to.
//
// It also does not belong to this browser. The conversation is stored, so the
// question asked on a laptop is answered into something a phone can open.
func (s *Server) handleAssistantChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID string `json:"chatId"`
		Text   string `json:"text"`
		// messages is the older shape: the whole conversation, sent every time,
		// kept nowhere. A client still using it gets what it always got.
		Messages []assistant.Message `json:"messages"`
		MaxSteps int                 `json:"maxSteps"`
		// ProviderID chooses the model, and only for a conversation that does
		// not have one yet.
		ProviderID string `json:"providerId"`
		// FileIDs are uploads to attach to this turn. Ids rather than paths,
		// so a request cannot name a file the person never uploaded.
		FileIDs []string `json:"fileIds"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	// Files on their own are a question: handing over a logo and nothing else
	// plainly means "use this", and making somebody type a word first would be
	// a rule for the machine's benefit.
	files, ferr := s.attachments(req.FileIDs)
	if ferr != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: ferr.Error()})
		return
	}
	if strings.TrimSpace(req.Text) == "" && len(files) == 0 && len(req.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "send a question"})
		return
	}
	u := userFrom(r.Context())
	actor, scopes, role := scopesForRequest(r.Context())

	// Which model this conversation is having. An existing conversation keeps
	// its own — it is part of what the transcript is — and only a new one takes
	// what the composer asked for. Resolving before anything is written means a
	// server with no model configured says so instead of leaving an empty
	// conversation behind.
	providerID := strings.TrimSpace(req.ProviderID)
	if strings.TrimSpace(req.ChatID) != "" && s.chats != nil {
		if ch, _, err := s.chats.Get(r.Context(), u.Username, req.ChatID); err == nil && ch.ProviderID != "" {
			providerID = ch.ProviderID
		}
	}
	p, providerID, err := s.pickProvider(r.Context(), providerID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "not_configured", Message: err.Error()})
		return
	}

	// Writes to the conversation outlive the request as much as the run does,
	// and must outlive the run being cancelled too: a stopped run still did
	// whatever it did, and that belongs in the transcript.
	persist := context.WithoutCancel(r.Context())

	chatID, msgs := req.ChatID, req.Messages
	if strings.TrimSpace(req.Text) != "" || len(files) > 0 {
		if s.chats == nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "conversations are not available"})
			return
		}
		if chatID == "" {
			ch, err := s.chats.CreateWith(persist, u.Username, chatTitle(req.Text, files), providerID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
				return
			}
			chatID = ch.ID
		}
		// One run at a time per conversation. Two loops appending to one
		// transcript would interleave into something neither meant.
		if active := s.runs.activeFor(chatID); active != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "busy", "message": "this conversation is still working; watch it instead of starting another",
				"runId": active.ID, "chatId": chatID,
			})
			return
		}
		ask := assistant.Message{Role: assistant.RoleUser, Text: req.Text, Files: files}
		if err := s.chats.Append(persist, u.Username, chatID, ask); err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
			return
		}
		_, history, err := s.chats.Get(persist, u.Username, chatID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
			return
		}
		msgs = history
	}

	// The values of the request context — who is asking, and under which token
	// — are what every tool call is checked against, so they have to travel
	// with the run. Its cancellation must not: that is the thing this exists to
	// survive. WithoutCancel keeps the first and drops the second.
	runCtx, cancel := context.WithCancel(persist)
	// A ceiling, so a run that never finishes is not immortal.
	runCtx, cancelTimeout := context.WithTimeout(runCtx, assistantRunCeiling)

	rn := s.runs.start(u.Username, chatID, lastQuestion(msgs), cancel)
	exec := s.assistantExecutor(runCtx, actor, scopes, role)
	tools := s.assistantTools()
	// The question travels in the first event so a client that reattaches after
	// a reload — a phone reopening the panel — can show what was asked without
	// waiting for the run to finish and hand over the whole transcript.
	// base is how many messages the conversation held before this run said
	// anything. A client reattaching replays the whole event log from zero — it
	// is the only way to see the tool calls that happened while it was away —
	// and turns this run has already written to the conversation would then be
	// counted twice. Trimming to base first makes the replay exact.
	rn.add("start", map[string]any{
		"runId": rn.ID, "chatId": chatID, "tools": len(tools),
		"ask": rn.Ask, "base": len(msgs),
	})

	obs := rn.observer()
	if chatID != "" && s.chats != nil {
		// Each turn is written as it completes rather than the transcript being
		// saved at the end, so a run cut short by a restart leaves behind what
		// it had already done.
		turn := obs.Turn
		obs.Turn = func(m assistant.Message) {
			turn(m)
			if err := s.chats.Append(persist, u.Username, chatID, m); err != nil {
				s.log.Warn("assistant: could not store a turn", "chat", chatID, "err", err)
			}
		}
	}

	go func() {
		defer cancelTimeout()
		defer cancel()
		out, err := assistant.RunStream(runCtx, p, s.systemPrompt(), msgs, tools, exec, req.MaxSteps, obs)
		switch {
		case err != nil && runCtx.Err() != nil && rn.cancelled():
			rn.add("error", map[string]any{"message": "stopped", "messages": out})
			rn.finish("cancelled")
		case err != nil:
			rn.add("error", map[string]any{"message": err.Error(), "messages": out})
			rn.finish("error")
		default:
			rn.add("done", map[string]any{"messages": out, "reply": assistant.Text(out)})
			rn.finish("done")
		}
	}()

	if !acceptsStream(r) {
		// An older client reads the whole body at once, so it waits for the
		// run and gets one document — the shape it has always parsed.
		out, err := rn.wait(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "provider", "message": err.Error(), "messages": out})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": out, "reply": assistant.Text(out), "runId": rn.ID, "chatId": chatID})
		return
	}
	streamRun(r.Context(), w, rn, 0)
}

// handleAssistantChats lists conversations, or starts an empty one.
func (s *Server) handleAssistantChats(w http.ResponseWriter, r *http.Request) {
	if s.chats == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	u := userFrom(r.Context())
	if r.Method == http.MethodPost {
		var req struct {
			Title      string `json:"title"`
			ProviderID string `json:"providerId"`
		}
		_ = decode(r, &req)
		// The resolved id, not the empty one: a conversation started today
		// against the default keeps that model when the default changes.
		providerID := strings.TrimSpace(req.ProviderID)
		if s.ai != nil {
			if p, err := s.ai.Resolve(r.Context(), providerID); err == nil && p != nil {
				providerID = p.ID
			}
		}
		ch, err := s.chats.CreateWith(r.Context(), u.Username, req.Title, providerID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, s.withRun(ch))
		return
	}
	list, err := s.chats.List(r.Context(), u.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, s.withRun(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// withRun says whether a conversation is working right now, so a client that
// has just opened on another device knows to watch rather than to ask again.
func (s *Server) withRun(ch *assistant.Chat) map[string]any {
	out := map[string]any{
		"id": ch.ID, "title": ch.Title, "messages": ch.Messages,
		"providerId": ch.ProviderID,
		"createdAt":  ch.CreatedAt, "updatedAt": ch.UpdatedAt,
	}
	if rn := s.runs.activeFor(ch.ID); rn != nil {
		_, events := rn.state()
		out["runId"], out["runEvents"] = rn.ID, events
	}
	return out
}

// handleAssistantChat1 reads, renames or deletes one conversation.
func (s *Server) handleAssistantChat1(w http.ResponseWriter, r *http.Request) {
	if s.chats == nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "conversations are not available"})
		return
	}
	u := userFrom(r.Context())
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodDelete:
		if err := s.chats.Delete(r.Context(), u.Username, id); err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
			return
		}
		// A conversation being deleted takes its run with it: there is nowhere
		// left to write the answer down.
		if rn := s.runs.activeFor(id); rn != nil {
			rn.stop()
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		var req struct {
			Title string `json:"title"`
		}
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		if err := s.chats.Rename(r.Context(), u.Username, id, req.Title); err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		ch, msgs, err := s.chats.Get(r.Context(), u.Username, id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
			return
		}
		out := s.withRun(ch)
		// "messages" is already the count in a listing, so the transcript has a
		// name of its own rather than the same key meaning two things.
		out["turns"] = msgs
		writeJSON(w, http.StatusOK, out)
	}
}

// handleAssistantRun reattaches to a run in progress, or reads a finished one.
//
// `from` is the sequence number the client already has, so a phone that comes
// back after five minutes is given what it missed and then follows along, with
// nothing repeated and nothing dropped.
func (s *Server) handleAssistantRun(w http.ResponseWriter, r *http.Request) {
	rn := s.runs.get(r.PathValue("id"))
	if rn == nil || rn.User != userFrom(r.Context()).Username {
		// A run belongs to whoever started it. Another admin cannot read one,
		// because a transcript carries whatever the tools returned.
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such run; it may have finished before the daemon last restarted"})
		return
	}
	from := 0
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "from must be a sequence number"})
			return
		}
		from = n
	}
	streamRun(r.Context(), w, rn, from)
}

// handleAssistantRuns lists this person's recent runs, so a client that lost
// its connection can find what it was watching.
func (s *Server) handleAssistantRuns(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, rn := range s.runs.list(userFrom(r.Context()).Username) {
		status, events := rn.state()
		out = append(out, map[string]any{
			"id": rn.ID, "status": status, "events": events,
			"ask": rn.Ask, "startedAt": rn.Started.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAssistantRunCancel stops a run.
func (s *Server) handleAssistantRunCancel(w http.ResponseWriter, r *http.Request) {
	rn := s.runs.get(r.PathValue("id"))
	if rn == nil || rn.User != userFrom(r.Context()).Username {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such run"})
		return
	}
	rn.stop()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// lastQuestion is what the person asked, for a list they are choosing from.
func lastQuestion(msgs []assistant.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == assistant.RoleUser && strings.TrimSpace(msgs[i].Text) != "" {
			return preview(msgs[i].Text)
		}
	}
	return ""
}

// acceptsStream reports whether the caller can read newline-delimited JSON.
//
// It exists for one client in particular: a panel tab that was open when the
// daemon was updated is still running the build that read the whole body with
// JSON.parse, and that fails on the newline after the first line — "Unexpected
// non-whitespace character after JSON at position 28", which names nothing a
// person could act on. It says `Accept: application/json` and means it, so it
// is given one JSON document.
//
// A client that says nothing, or */* — curl, a script — gets the stream, since
// the timeout the stream exists for is theirs too.
func acceptsStream(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	switch {
	case accept == "":
		return true
	case strings.Contains(accept, "application/x-ndjson"):
		return true
	case strings.Contains(accept, "application/json"):
		// Asked for JSON and never mentioned the stream: an older client.
		return false
	}
	return true
}

// assistantRunCeiling is how long a run may take before it is abandoned. Long,
// because a deploy with a cold build genuinely takes a while; finite, because
// nothing should run on this server forever without somebody having asked.
const assistantRunCeiling = 30 * time.Minute

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
func streamRun(ctx context.Context, w http.ResponseWriter, rn *run, from int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Nothing can be streamed to this writer, and the run is already going,
		// so say where to find it rather than block.
		writeJSON(w, http.StatusOK, map[string]any{"runId": rn.ID, "streaming": false})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	// Nothing between here and the client may buffer this, or the point is
	// lost and the proxy times out with the whole reply sitting in a buffer.
	// no-transform is the load-bearing half: a compressor in front of a stream
	// holds its first kilobyte back, and for a stream that is however long the
	// work takes. It reached the browser as nothing, then as a gateway timeout.
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	send := func(v any) bool {
		if err := enc.Encode(v); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	tick := time.NewTicker(assistantHeartbeat)
	defer tick.Stop()
	seq := from
	for {
		events, changed, running := rn.since(seq)
		for _, e := range events {
			if !send(e) {
				return
			}
			seq = e.Seq + 1
		}
		if !running {
			// Everything a finished run will ever have said has been sent.
			return
		}
		select {
		case <-changed:
		case <-tick.C:
			// A single tool call can be a deploy that builds for five minutes,
			// and nothing else has anything to say while it does. The proxy in
			// front counts silence, not progress.
			if !send(map[string]any{"type": "ping"}) {
				return
			}
		case <-ctx.Done():
			// The client went away. The run does not care.
			return
		}
	}
}

// claudePath is where Claude Code is, for a panel that needs to tell somebody
// what to run.
func claudePath(ctx contextCtx, s *Server) string {
	if s.workspaces == nil {
		return ""
	}
	return s.workspaces.ClaudePath(ctx)
}

// attachments turns the ids a composer sent into what the model is told.
//
// Ids, not paths: the request says which uploads to attach, and the server
// looks up where they are. A request that could name a path would be a request
// that could attach /etc/shadow to a conversation and have the assistant read
// it out.
func (s *Server) attachments(ids []string) ([]assistant.Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s.uploads == nil {
		return nil, fmt.Errorf("uploads are not available on this server")
	}
	out := make([]assistant.Attachment, 0, len(ids))
	for _, id := range ids {
		f, err := s.uploads.Get(id)
		if err != nil {
			return nil, fmt.Errorf("that upload is no longer there; attach it again")
		}
		out = append(out, assistant.Attachment{Name: f.Name, Path: f.Path, Size: f.Size, Type: f.Type})
	}
	return out, nil
}

// chatTitle names a conversation from what started it. A conversation opened
// by dropping in three images has no words to be named after, so it is named
// after the images.
func chatTitle(text string, files []assistant.Attachment) string {
	if strings.TrimSpace(text) != "" {
		return text
	}
	switch len(files) {
	case 0:
		return ""
	case 1:
		return files[0].Name
	default:
		return fmt.Sprintf("%s and %d more", files[0].Name, len(files)-1)
	}
}

// systemPrompt is what the model is told it is, plus whatever this particular
// server adds to that.
func (s *Server) systemPrompt() string {
	if s.uploads == nil {
		return assistantSystem
	}
	return assistantSystem + fmt.Sprintf(assistantFiles, s.uploads.Dir())
}
