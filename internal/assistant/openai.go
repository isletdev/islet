package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI talks to any endpoint that speaks the OpenAI chat-completions shape,
// which is most of them: OpenAI itself, Groq, Together, DeepSeek, Mistral,
// OpenRouter, and anything self-hosted behind Ollama, vLLM or llama.cpp. That
// is the reason this provider exists rather than one per vendor — the differences
// between them are a base URL and a model name.
//
// POST {base}/chat/completions with a bearer key; tools are functions whose
// arguments come back as a JSON *string* rather than an object, which is the
// one place this format differs from Anthropic's in a way that matters.
type OpenAI struct {
	Key       string
	Model     string
	BaseURL   string // https://api.openai.com/v1, http://localhost:11434/v1, …
	MaxTokens int
	Label     string // what to call it in the panel
	HTTP      *http.Client
}

func (o *OpenAI) Name() string {
	if o.Label != "" {
		return o.Label
	}
	return "OpenAI-compatible"
}

func (o *OpenAI) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (o *OpenAI) base() string {
	if o.BaseURL != "" {
		return strings.TrimSuffix(o.BaseURL, "/")
	}
	return "https://api.openai.com/v1"
}

func (o *OpenAI) Complete(ctx context.Context, system string, msgs []Message, tools []Tool) (Message, error) {
	if strings.TrimSpace(o.Model) == "" {
		return Message{}, fmt.Errorf("no model is set for %s", o.Name())
	}
	body := map[string]any{"model": o.Model, "messages": openAIMessages(system, msgs)}
	if o.MaxTokens > 0 {
		body["max_tokens"] = o.MaxTokens
	}
	if len(tools) > 0 {
		ts := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			ts = append(ts, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema,
			}})
		}
		body["tools"] = ts
	}

	b, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base()+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("content-type", "application/json")
	// A local model behind Ollama or llama.cpp usually wants no key at all.
	if strings.TrimSpace(o.Key) != "" {
		req.Header.Set("authorization", "Bearer "+o.Key)
	}

	resp, err := o.client().Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("%s: %s: %s", o.Name(), resp.Status, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, fmt.Errorf("%s: unreadable response: %w", o.Name(), err)
	}
	if len(out.Choices) == 0 {
		return Message{}, fmt.Errorf("%s returned no choices", o.Name())
	}
	c := out.Choices[0].Message
	msg := Message{Role: RoleAssistant, Text: c.Content}
	for _, tc := range c.ToolCalls {
		in := map[string]any{}
		// Arguments are a JSON string here, not an object. A model that emits
		// something unparseable should reach the tool as empty arguments and
		// be told what was wrong, rather than failing the whole turn.
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &in)
		}
		msg.Calls = append(msg.Calls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: in})
	}
	return msg, nil
}

// openAIMessages renders the conversation. The system prompt is a message
// here rather than a field, and each tool result is its own message keyed by
// the call id — both differ from Anthropic's shape.
func openAIMessages(system string, msgs []Message) []map[string]any {
	out := []map[string]any{}
	if strings.TrimSpace(system) != "" {
		out = append(out, map[string]any{"role": "system", "content": system})
	}
	for _, m := range msgs {
		switch {
		case len(m.Results) > 0:
			for _, r := range m.Results {
				out = append(out, map[string]any{"role": "tool", "tool_call_id": r.CallID, "content": r.Content})
			}
		case len(m.Calls) > 0:
			calls := make([]map[string]any, 0, len(m.Calls))
			for _, c := range m.Calls {
				args, _ := json.Marshal(c.Input)
				calls = append(calls, map[string]any{"id": c.ID, "type": "function",
					"function": map[string]any{"name": c.Name, "arguments": string(args)}})
			}
			out = append(out, map[string]any{"role": RoleAssistant, "content": m.Text, "tool_calls": calls})
		default:
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			out = append(out, map[string]any{"role": m.Role, "content": m.Text})
		}
	}
	return out
}
