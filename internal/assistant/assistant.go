// Package assistant runs a conversation with a language model that can act on
// this server through Islet's own tools.
//
// It speaks to providers over plain HTTP rather than through their SDKs. Two
// reasons, in this order: the daemon is a single static binary that has to run
// on a 1 vCPU box and its dependency list is deliberately short, and there is
// more than one provider here — taking a vendor SDK for one and hand-writing
// the rest would leave two shapes of the same thing to maintain. The wire
// formats are small and stable, and each provider file says which document it
// was written from.
package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Role is who said something. Tool results are carried on a user message,
// which is how both wire formats represent them.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// ToolCall is the model asking for a tool to be run.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult is the answer, on its way back.
type ToolResult struct {
	CallID  string `json:"callId"`
	Content string `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

// Message is one turn. A turn from the model may carry text, tool calls or
// both; a turn from the user carries text or the results of the calls.
type Message struct {
	Role    string       `json:"role"`
	Text    string       `json:"text,omitempty"`
	Calls   []ToolCall   `json:"calls,omitempty"`
	Results []ToolResult `json:"results,omitempty"`
}

// Tool is what the model is told it can do. Schema is JSON Schema, which is
// what every provider wants, and what Islet's MCP tools already carry.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Provider is one model behind one API.
type Provider interface {
	// Name is what the panel shows.
	Name() string
	// Complete sends the conversation and returns the model's next turn.
	Complete(ctx context.Context, system string, msgs []Message, tools []Tool) (Message, error)
}

// Executor runs a tool the model asked for. An error is returned to the model
// as a failed result rather than ending the run: a model that is told its call
// failed usually tries something else, and a run that stops on the first
// refused tool would be useless on a token that is deliberately narrow.
type Executor func(ctx context.Context, call ToolCall) (string, error)

// Run drives the conversation until the model stops asking for tools.
//
// maxSteps bounds it. Every step is a paid request and a set of calls against
// a real server, and a model that has misunderstood its task can ask for the
// same thing indefinitely; stopping with an explanation is better than either
// looping or pretending the answer is complete.
func Run(ctx context.Context, p Provider, system string, msgs []Message, tools []Tool, exec Executor, maxSteps int) ([]Message, error) {
	if p == nil {
		return msgs, errors.New("no assistant provider is configured")
	}
	if maxSteps <= 0 {
		maxSteps = 12
	}
	for step := 0; step < maxSteps; step++ {
		reply, err := p.Complete(ctx, system, msgs, tools)
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, reply)
		if len(reply.Calls) == 0 {
			return msgs, nil
		}
		results := make([]ToolResult, 0, len(reply.Calls))
		for _, c := range reply.Calls {
			// Every call gets a result, including the ones that fail. Dropping
			// one leaves the model with a call it never heard back about, and
			// both wire formats reject a turn whose results do not line up
			// with the calls that preceded it.
			out, err := exec(ctx, c)
			if err != nil {
				results = append(results, ToolResult{CallID: c.ID, Content: err.Error(), IsError: true})
				continue
			}
			results = append(results, ToolResult{CallID: c.ID, Content: out})
		}
		msgs = append(msgs, Message{Role: RoleUser, Results: results})
	}
	msgs = append(msgs, Message{
		Role: RoleAssistant,
		Text: fmt.Sprintf("I stopped after %d rounds of tool calls without finishing. Ask me to continue, or narrow the task.", maxSteps),
	})
	return msgs, nil
}

// Text is the last thing the model actually said, which is what a caller
// wanting one answer rather than a transcript is after.
func Text(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant && strings.TrimSpace(msgs[i].Text) != "" {
			return msgs[i].Text
		}
	}
	return ""
}
