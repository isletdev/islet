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

// The observer exists so a caller can put a turn on somebody's screen while the
// run is still going. A version that collected the turns and replayed them at
// the end would pass a test that only compared the final slice, and would still
// leave a proxy in front of the panel waiting long enough to give up — which is
// the bug this was written for. So the check is about *when*: by the time a tool
// runs, the turn that asked for it must already have been reported.
func TestRunStreamReportsEachTurnWhileTheRunIsStillGoing(t *testing.T) {
	p := &fake{turns: []Message{
		{Role: RoleAssistant, Calls: []ToolCall{{ID: "a", Name: "deploy_app"}}},
		{Role: RoleAssistant, Text: "Deployed."},
	}}
	var seen []Message
	var atToolTime int
	out, err := RunStream(context.Background(), p, "", []Message{{Role: RoleUser, Text: "deploy it"}}, nil,
		func(context.Context, ToolCall) (string, error) { atToolTime = len(seen); return "ok", nil }, 10,
		&Observer{Turn: func(m Message) { seen = append(seen, m) }})
	if err != nil {
		t.Fatal(err)
	}
	if atToolTime != 1 {
		t.Errorf("turns reported before the tool ran = %d, want 1: the caller learns nothing until the end", atToolTime)
	}
	// Every turn the run produced, in the order it produced them: the caller
	// can append them and end up with the same transcript Run returns.
	if len(seen) != len(out)-1 {
		t.Fatalf("reported %d turns, run produced %d (one of which is the question)", len(seen), len(out))
	}
	for i, m := range seen {
		if want := out[i+1]; m.Text != want.Text || len(m.Calls) != len(want.Calls) || len(m.Results) != len(want.Results) {
			t.Errorf("turn %d reported as %+v, transcript has %+v", i, m, want)
		}
	}
}

// A run that fails partway still has to have reported what it got through, or
// the panel shows nothing at all for a conversation that did real work.
func TestRunStreamReportsTurnsBeforeAFailure(t *testing.T) {
	p := &fake{turns: []Message{{Role: RoleAssistant, Calls: []ToolCall{{ID: "a", Name: "list_apps"}}}}, err: nil}
	var seen []Message
	// The first call succeeds, the second fails: the provider breaking mid-run.
	p2 := &failAfter{inner: p, after: 1}
	_, err := RunStream(context.Background(), p2, "", []Message{{Role: RoleUser, Text: "list them"}}, nil,
		func(context.Context, ToolCall) (string, error) { return "[]", nil }, 10,
		&Observer{Turn: func(m Message) { seen = append(seen, m) }})
	if err == nil {
		t.Fatal("want the provider error")
	}
	if len(seen) == 0 {
		t.Error("nothing was reported, so a failed run shows an empty screen")
	}
}

type failAfter struct {
	inner *fake
	after int
	n     int
}

func (f *failAfter) Name() string { return "failAfter" }
func (f *failAfter) Complete(ctx context.Context, sys string, msgs []Message, tools []Tool) (Message, error) {
	f.n++
	if f.n > f.after {
		return Message{}, errors.New("provider went away")
	}
	return f.inner.Complete(ctx, sys, msgs, tools)
}
