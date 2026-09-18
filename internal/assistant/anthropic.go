package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAnthropicModel is what a new configuration gets. Opus 5 thinks
// adaptively on its own, so no thinking parameter is sent: asking for a
// token budget is rejected on this generation, and disabling thinking makes
// it occasionally write a tool call into its visible text instead of calling
// the tool, which in a loop like this one would look like the model ignoring
// its instructions.
const DefaultAnthropicModel = "claude-opus-5"

// anthropicVersion is the dated API contract, sent on every request. It is not
// the model and does not change when the model does.
const anthropicVersion = "2023-06-01"

// Anthropic talks to the Messages API with an API key.
//
// Written from the Messages API reference: POST /v1/messages with x-api-key,
// anthropic-version and a JSON body of model, max_tokens, system, tools and
// messages; the reply is a list of content blocks, of which text and tool_use
// are the ones that matter here.
type Anthropic struct {
	Key       string
	Model     string
	BaseURL   string // for a gateway or a proxy; the public API by default
	MaxTokens int
	HTTP      *http.Client
}

func (a *Anthropic) Name() string { return "Anthropic" }

// Ready: an API key, which is the whole of what this needs.
func (a *Anthropic) Ready() error {
	if strings.TrimSpace(a.Key) == "" {
		return errors.New("this model has no API key")
	}
	return nil
}

func (a *Anthropic) model() string {
	if a.Model != "" {
		return a.Model
	}
	return DefaultAnthropicModel
}

func (a *Anthropic) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	// A model working through several tools takes its time; the default client
	// has no timeout at all, which would hang a request forever.
	return &http.Client{Timeout: 10 * time.Minute}
}

func (a *Anthropic) base() string {
	if a.BaseURL != "" {
		return strings.TrimSuffix(a.BaseURL, "/")
	}
	return "https://api.anthropic.com"
}

func (a *Anthropic) Complete(ctx context.Context, system string, msgs []Message, tools []Tool) (Message, error) {
	if strings.TrimSpace(a.Key) == "" {
		return Message{}, fmt.Errorf("no Anthropic API key is set")
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		// Enough for a real answer without risking an HTTP timeout on a
		// non-streaming request.
		maxTokens = 16000
	}

	body := map[string]any{
		"model":      a.model(),
		"max_tokens": maxTokens,
		"messages":   anthropicMessages(msgs),
	}
	if strings.TrimSpace(system) != "" {
		body["system"] = system
	}
	if len(tools) > 0 {
		ts := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			ts = append(ts, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
		}
		body["tools"] = ts
	}

	b, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/v1/messages", bytes.NewReader(b))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.Key)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.client().Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("anthropic: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var out struct {
		StopReason  string `json:"stop_reason"`
		StopDetails struct {
			Category    string `json:"category"`
			Explanation string `json:"explanation"`
		} `json:"stop_details"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, fmt.Errorf("anthropic: unreadable response: %w", err)
	}
	// A refusal arrives as a 200 with nothing useful in content, so it has to
	// be checked before the blocks are read or it reads as an empty answer.
	if out.StopReason == "refusal" {
		why := out.StopDetails.Explanation
		if why == "" {
			why = "the request was declined"
		}
		return Message{}, fmt.Errorf("anthropic declined this request (%s): %s", out.StopDetails.Category, why)
	}

	msg := Message{Role: RoleAssistant}
	for _, c := range out.Content {
		switch c.Type {
		case "text":
			msg.Text += c.Text
		case "tool_use":
			in := map[string]any{}
			// Arguments are parsed, never string-matched: the escaping of the
			// JSON in this field is not guaranteed to be stable.
			if len(c.Input) > 0 {
				_ = json.Unmarshal(c.Input, &in)
			}
			msg.Calls = append(msg.Calls, ToolCall{ID: c.ID, Name: c.Name, Input: in})
		}
	}
	return msg, nil
}

// anthropicMessages renders the conversation. Tool results go on a user turn,
// as content blocks, which is what this API expects.
func anthropicMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case len(m.Results) > 0:
			blocks := make([]map[string]any, 0, len(m.Results))
			for _, r := range m.Results {
				b := map[string]any{"type": "tool_result", "tool_use_id": r.CallID, "content": r.Content}
				if r.IsError {
					b["is_error"] = true
				}
				blocks = append(blocks, b)
			}
			out = append(out, map[string]any{"role": RoleUser, "content": blocks})
		case len(m.Calls) > 0:
			blocks := []map[string]any{}
			if strings.TrimSpace(m.Prompt()) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Prompt()})
			}
			for _, c := range m.Calls {
				in := c.Input
				if in == nil {
					in = map[string]any{}
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": in})
			}
			out = append(out, map[string]any{"role": RoleAssistant, "content": blocks})
		default:
			if strings.TrimSpace(m.Prompt()) == "" {
				continue
			}
			out = append(out, map[string]any{"role": m.Role, "content": m.Prompt()})
		}
	}
	return out
}
