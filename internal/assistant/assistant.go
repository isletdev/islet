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
	"time"
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

// Attachment is a file the person handed the assistant.
//
// Path is the whole of it. The file is already on this server by the time a
// message carries one, so the model needs no way to fetch it and no encoding
// on the wire — it passes the path to any of the tools it already has, and
// Claude Code opens it directly. That is why a photograph and a forty-megabyte
// video cost the same to attach.
type Attachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
	Type string `json:"type,omitempty"`
}

// Message is one turn. A turn from the model may carry text, tool calls or
// both; a turn from the user carries text, files, or the results of the calls.
type Message struct {
	Role    string       `json:"role"`
	Text    string       `json:"text,omitempty"`
	Files   []Attachment `json:"files,omitempty"`
	Calls   []ToolCall   `json:"calls,omitempty"`
	Results []ToolResult `json:"results,omitempty"`
	// Tools is what was done while this turn was being produced, by a provider
	// that runs its own loop and therefore never returns Calls. It is a record,
	// not an instruction: nothing here is ever executed, and the loop ignores
	// it. Without it a conversation with the subscription provider reads, on
	// the device it is opened on tomorrow, as prose with no account of what it
	// actually did to the server.
	Tools []ToolRun `json:"tools,omitempty"`
}

// ToolRun is one call that already happened.
type ToolRun struct {
	Name   string         `json:"name"`
	Input  map[string]any `json:"input,omitempty"`
	MS     int64          `json:"ms"`
	OK     bool           `json:"ok"`
	Output string         `json:"output,omitempty"`
}

// Prompt is a turn's text as the model should receive it: what was typed, and
// then where the attached files are.
//
// Folded in here rather than at each provider, because there are three of them
// and this is the kind of difference that becomes a bug report about one model
// ignoring attachments. The panel keeps the two apart — the words are the
// person's, the list is Islet's — so the transcript shows files as files
// rather than as a paragraph somebody appears to have typed.
func (m Message) Prompt() string {
	if len(m.Files) == 0 {
		return m.Text
	}
	var b strings.Builder
	b.WriteString(m.Text)
	if strings.TrimSpace(m.Text) != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("Files attached to this message. They are already on this server, at these exact paths:\n")
	for _, f := range m.Files {
		b.WriteString("- ")
		b.WriteString(f.Path)
		if f.Type != "" {
			b.WriteString(" (" + f.Type)
			if f.Size > 0 {
				b.WriteString(", " + humanSize(f.Size))
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// humanSize is for the model's benefit as much as a reader's: "41 MB" is the
// difference between a logo and a video, and it decides whether reading the
// whole file is a sensible thing to do.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
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

// Observer watches a run while it is happening. Every field may be nil.
//
// It exists because "Working…" is not an answer. A deploy is a dozen tool calls
// and several minutes, and somebody watching a blank screen cannot tell a slow
// build from a wedged one — so each call is announced as it starts and again
// when it finishes, with how long it took and whether it worked.
type Observer struct {
	// Turn is a completed message: the model's reply, or the results going
	// back to it.
	Turn func(Message)
	// Text is something the model said on its way to a tool call. Providers
	// that answer in one piece never call it.
	Text func(string)
	// ToolStart is a call about to be made, ToolEnd the same call with its
	// answer and how long it took.
	ToolStart func(ToolCall)
	ToolEnd   func(ToolCall, ToolResult, time.Duration)
}

func (o *Observer) turn(m Message) {
	if o != nil && o.Turn != nil {
		o.Turn(m)
	}
}

func (o *Observer) text(t string) {
	if o != nil && o.Text != nil && strings.TrimSpace(t) != "" {
		o.Text(t)
	}
}

func (o *Observer) toolStart(c ToolCall) {
	if o != nil && o.ToolStart != nil {
		o.ToolStart(c)
	}
}

func (o *Observer) toolEnd(c ToolCall, r ToolResult, d time.Duration) {
	if o != nil && o.ToolEnd != nil {
		o.ToolEnd(c, r, d)
	}
}

// StreamingProvider is a provider that runs its own tool loop and can say what
// it is doing while it does it.
//
// The subscription provider is the one that needs this. Claude Code calls
// Islet's tools itself, inside a single Complete, so without a way to report
// from in there a run that made nine calls would reach the panel as one turn
// arriving several minutes later with nothing in between.
type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, system string, msgs []Message, tools []Tool, obs *Observer) (Message, error)
}

// complete asks the provider for the next turn, letting it report progress
// when it is able to.
func complete(ctx context.Context, p Provider, system string, msgs []Message, tools []Tool, obs *Observer) (Message, error) {
	if sp, ok := p.(StreamingProvider); ok {
		return sp.CompleteStream(ctx, system, msgs, tools, obs)
	}
	return p.Complete(ctx, system, msgs, tools)
}

// Run drives the conversation until the model stops asking for tools.
//
// maxSteps bounds it. Every step is a paid request and a set of calls against
// a real server, and a model that has misunderstood its task can ask for the
// same thing indefinitely; stopping with an explanation is better than either
// looping or pretending the answer is complete.
func Run(ctx context.Context, p Provider, system string, msgs []Message, tools []Tool, exec Executor, maxSteps int) ([]Message, error) {
	return RunStream(ctx, p, system, msgs, tools, exec, maxSteps, nil)
}

// RunStream is Run with an observer called as each turn completes.
//
// It exists because the whole loop can take minutes — a deploy is several tool
// calls and a build — and anything watching needs to know it is still working.
// A proxy in front of the panel is the sharper reason: Cloudflare gives an
// origin 100 seconds to respond and then answers 524, so a request that waits
// for the whole conversation is a request that fails on exactly the tasks
// worth asking for. Emitting each turn as it happens keeps bytes moving.
func RunStream(ctx context.Context, p Provider, system string, msgs []Message, tools []Tool, exec Executor, maxSteps int, obs *Observer) ([]Message, error) {
	emit := obs.turn
	if p == nil {
		return msgs, errors.New("no assistant provider is configured")
	}
	if maxSteps <= 0 {
		maxSteps = 12
	}
	for step := 0; step < maxSteps; step++ {
		reply, err := complete(ctx, p, system, msgs, tools, obs)
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, reply)
		emit(reply)
		if len(reply.Calls) == 0 {
			return msgs, nil
		}
		results := make([]ToolResult, 0, len(reply.Calls))
		for _, c := range reply.Calls {
			// Every call gets a result, including the ones that fail. Dropping
			// one leaves the model with a call it never heard back about, and
			// both wire formats reject a turn whose results do not line up
			// with the calls that preceded it.
			obs.toolStart(c)
			started := time.Now()
			out, err := exec(ctx, c)
			res := ToolResult{CallID: c.ID, Content: out}
			if err != nil {
				res = ToolResult{CallID: c.ID, Content: err.Error(), IsError: true}
			}
			obs.toolEnd(c, res, time.Since(started))
			results = append(results, res)
		}
		done := Message{Role: RoleUser, Results: results}
		msgs = append(msgs, done)
		emit(done)
	}
	msgs = append(msgs, Message{
		Role: RoleAssistant,
		Text: fmt.Sprintf("I stopped after %d rounds of tool calls without finishing. Ask me to continue, or narrow the task.", maxSteps),
	})
	emit(msgs[len(msgs)-1])
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
