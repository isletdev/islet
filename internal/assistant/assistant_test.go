package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fake is a provider that replays a script, so the loop can be tested without
// a network, a key or a bill.
type fake struct {
	turns []Message
	seen  [][]Message
	err   error
}

func (f *fake) Name() string { return "fake" }
func (f *fake) Complete(_ context.Context, _ string, msgs []Message, _ []Tool) (Message, error) {
	f.seen = append(f.seen, append([]Message(nil), msgs...))
	if f.err != nil {
		return Message{}, f.err
	}
	if len(f.turns) == 0 {
		return Message{Role: RoleAssistant, Text: "nothing left to say"}, nil
	}
	t := f.turns[0]
	f.turns = f.turns[1:]
	return t, nil
}

func TestRunStopsWhenTheModelStopsCallingTools(t *testing.T) {
	p := &fake{turns: []Message{
		{Role: RoleAssistant, Calls: []ToolCall{{ID: "a", Name: "list_domains"}}},
		{Role: RoleAssistant, Text: "There are two domains."},
	}}
	var ran []string
	out, err := Run(context.Background(), p, "sys", []Message{{Role: RoleUser, Text: "what domains?"}}, nil,
		func(_ context.Context, c ToolCall) (string, error) { ran = append(ran, c.Name); return "[]", nil }, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "list_domains" {
		t.Errorf("tools run: %v", ran)
	}
	if got := Text(out); got != "There are two domains." {
		t.Errorf("final text %q", got)
	}
}

// A tool that fails is part of the conversation, not the end of it: a narrow
// token refusing one call is the normal case, and the model should be able to
// try something else.
func TestAFailedToolIsReportedNotFatal(t *testing.T) {
	p := &fake{turns: []Message{
		{Role: RoleAssistant, Calls: []ToolCall{{ID: "a", Name: "read_file"}}},
		{Role: RoleAssistant, Text: "I cannot read files with this token."},
	}}
	out, err := Run(context.Background(), p, "", []Message{{Role: RoleUser, Text: "read /etc/hosts"}}, nil,
		func(context.Context, ToolCall) (string, error) { return "", errors.New("scopes do not cover files") }, 10)
	if err != nil {
		t.Fatalf("a refused tool must not end the run: %v", err)
	}
	// The refusal has to reach the model, or it is answering blind.
	var sawResult bool
	for _, m := range out {
		for _, r := range m.Results {
			if r.IsError && strings.Contains(r.Content, "scopes do not cover") {
				sawResult = true
			}
		}
	}
	if !sawResult {
		t.Error("the failure should be sent back as a tool result")
	}
}

// Every call gets a result, including the ones that failed: a turn whose
// results do not line up with its calls is rejected by both wire formats.
func TestEveryCallGetsAResult(t *testing.T) {
	p := &fake{turns: []Message{
		{Role: RoleAssistant, Calls: []ToolCall{{ID: "a", Name: "one"}, {ID: "b", Name: "two"}, {ID: "c", Name: "three"}}},
		{Role: RoleAssistant, Text: "done"},
	}}
	out, _ := Run(context.Background(), p, "", []Message{{Role: RoleUser, Text: "go"}}, nil,
		func(_ context.Context, c ToolCall) (string, error) {
			if c.Name == "two" {
				return "", errors.New("nope")
			}
			return "ok", nil
		}, 10)
	for _, m := range out {
		if len(m.Results) == 0 {
			continue
		}
		if len(m.Results) != 3 {
			t.Fatalf("three calls should produce three results, got %d", len(m.Results))
		}
		ids := map[string]bool{}
		for _, r := range m.Results {
			ids[r.CallID] = true
		}
		for _, want := range []string{"a", "b", "c"} {
			if !ids[want] {
				t.Errorf("no result for call %q", want)
			}
		}
	}
}

// A model that has misunderstood its task can ask for the same thing forever.
// Stopping and saying so beats looping, and beats pretending to be finished.
func TestRunIsBounded(t *testing.T) {
	var turns []Message
	for i := 0; i < 50; i++ {
		turns = append(turns, Message{Role: RoleAssistant, Calls: []ToolCall{{ID: "x", Name: "again"}}})
	}
	p := &fake{turns: turns}
	calls := 0
	out, err := Run(context.Background(), p, "", []Message{{Role: RoleUser, Text: "go"}}, nil,
		func(context.Context, ToolCall) (string, error) { calls++; return "ok", nil }, 4)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Errorf("should have stopped after 4 rounds, ran %d", calls)
	}
	if !strings.Contains(Text(out), "stopped after 4 rounds") {
		t.Errorf("the transcript should say why it stopped, got %q", Text(out))
	}
}

func TestAProviderErrorEndsTheRun(t *testing.T) {
	p := &fake{err: errors.New("401 invalid api key")}
	if _, err := Run(context.Background(), p, "", []Message{{Role: RoleUser, Text: "hi"}}, nil,
		func(context.Context, ToolCall) (string, error) { return "", nil }, 5); err == nil {
		t.Fatal("a provider error should be returned, not swallowed")
	}
}

func TestNoProviderIsAClearError(t *testing.T) {
	if _, err := Run(context.Background(), nil, "", nil, nil, nil, 5); err == nil ||
		!strings.Contains(err.Error(), "no assistant provider") {
		t.Errorf("want a clear error, got %v", err)
	}
}
