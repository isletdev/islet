package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/isletdev/islet/internal/ai"
	"github.com/isletdev/islet/internal/assistant"
	"github.com/isletdev/islet/pkg/api"
)

// The models a server is set up to use, and how a conversation gets one.
//
// Everything here exists so that "which model" is asked once, when a chat or an
// agent starts, and never again. The API layer owns the credential: providers
// are stored sealed and the daemon's keys live here, so this file is the only
// place a key is in the clear and only for as long as one request needs it.

func (s *Server) handleAIProviders(w http.ResponseWriter, r *http.Request) {
	if s.ai == nil {
		writeJSON(w, http.StatusOK, []ai.Provider{})
		return
	}
	if r.Method == http.MethodGet {
		list, err := s.ai.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, list)
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Model     string `json:"model"`
		BaseURL   string `json:"baseUrl"`
		Key       string `json:"key"`
		Command   string `json:"command"`
		MCPConfig string `json:"mcpConfig"`
		Default   bool   `json:"default"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	p := &ai.Provider{
		ID: req.ID, Name: req.Name, Kind: req.Kind, Model: req.Model, BaseURL: req.BaseURL,
		Command: req.Command, MCPConfig: req.MCPConfig, Default: req.Default,
	}
	// An empty key leaves whatever is stored alone: the panel never shows a
	// credential, so it cannot send one back, and saving a model must not wipe
	// the key that model is reached with.
	if k := strings.TrimSpace(req.Key); k != "" {
		sealed, err := s.seal(k)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		p.SealedKey = sealed
	}
	if err := s.ai.Save(r.Context(), p); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "ai.provider.save", p.Name, p.Kind+" "+p.Model)
	saved, err := s.ai.Get(r.Context(), p.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleAIProvider(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) || s.ai == nil {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodDelete:
		p, err := s.ai.Get(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such provider"})
			return
		}
		if err := s.ai.Delete(r.Context(), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "ai.provider.delete", p.Name, "")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost: // make default
		if err := s.ai.SetDefault(r.Context(), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		list, _ := s.ai.List(r.Context())
		writeJSON(w, http.StatusOK, list)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, api.Error{Error: "method", Message: "unsupported"})
	}
}

// build turns a stored provider into something that can hold a conversation.
func (s *Server) build(ctx context.Context, p *ai.Provider) (assistant.Provider, error) {
	key, err := s.unseal(p.SealedKey)
	if err != nil {
		return nil, err
	}
	switch p.Kind {
	case ai.KindOpenAI:
		return &assistant.OpenAI{Key: key, Model: p.Model, BaseURL: p.BaseURL, Label: p.Name}, nil
	case ai.KindSubscription:
		// No key: this one spends the subscription somebody signed into on this
		// server. The MCP configuration is what gives it Islet's tools, and the
		// token inside it is what bounds them.
		bin := p.Command
		if bin == "" && s.workspaces != nil {
			bin = s.workspaces.ClaudePath(ctx)
		}
		return &assistant.Subscription{Bin: bin, MCPConfig: p.MCPConfig, Model: p.Model}, nil
	default:
		return &assistant.Anthropic{Key: key, Model: p.Model, BaseURL: p.BaseURL}, nil
	}
}

// AgentEnv is what a workspace agent needs in its environment to reach the
// model it was created with.
//
// Only an API key has anything to pass: a subscription is credentials Claude
// Code already holds on this server, and an OpenAI-compatible endpoint has no
// terminal agent to drive. The value is handed to tmux when the window is
// created, so it never passes through a shell where it would sit in the
// scrollback and the history.
func (s *Server) AgentEnv(ctx context.Context, providerID string) map[string]string {
	if s.ai == nil || strings.TrimSpace(providerID) == "" {
		return nil
	}
	p, err := s.ai.Get(ctx, providerID)
	if err != nil || p.Kind != ai.KindAnthropic {
		return nil
	}
	key, err := s.unseal(p.SealedKey)
	if err != nil || key == "" {
		return nil
	}
	env := map[string]string{"ANTHROPIC_API_KEY": key}
	if p.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = p.BaseURL
	}
	if p.Model != "" {
		env["ANTHROPIC_MODEL"] = p.Model
	}
	return env
}

// AgentCommand is the command a provider implies for a workspace agent, or ""
// when that provider cannot drive one.
//
// A workspace agent is a program in a tmux window, so the question is which
// program: Claude Code, whether it is paid for by a subscription or by an API
// key. An OpenAI-compatible endpoint answers HTTP and has no CLI here, which
// is why the picker does not offer it rather than offering something that
// would fail on start.
func AgentCommand(p *ai.Provider) string {
	switch p.Kind {
	case ai.KindSubscription:
		if p.Command != "" {
			return p.Command
		}
		return "claude"
	case ai.KindAnthropic:
		return "claude"
	}
	return ""
}
