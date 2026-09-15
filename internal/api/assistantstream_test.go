package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/assistant"
)

// held is a provider whose first turn waits to be released, standing in for the
// minutes a real one spends on a deploy.
type held struct {
	release chan struct{}
	turns   []assistant.Message
}

func (h *held) Name() string { return "held" }
func (h *held) Complete(ctx context.Context, _ string, _ []assistant.Message, _ []assistant.Tool) (assistant.Message, error) {
	if h.release != nil {
		select {
		case <-h.release:
			h.release = nil
		case <-ctx.Done():
			return assistant.Message{}, ctx.Err()
		}
	}
	if len(h.turns) == 0 {
		return assistant.Message{Role: assistant.RoleAssistant, Text: "done"}, nil
	}
	t := h.turns[0]
	h.turns = h.turns[1:]
	return t, nil
}

// The bug this exists for: the endpoint answered once, at the end, and a
// conversation that deploys something runs long enough for Cloudflare to give
// up on the origin and return 524 to the person waiting. So what matters is not
// the final JSON but that bytes reach the client while the loop is still going
// — and keep reaching it through a single tool call that takes minutes.
func TestAssistantStreamKeepsTheConnectionFedWhileItWorks(t *testing.T) {
	old := assistantHeartbeat
	assistantHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() { assistantHeartbeat = old })

	h := &held{release: make(chan struct{}), turns: []assistant.Message{
		{Role: assistant.RoleAssistant, Text: "deployed"},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !streamAssistantRun(r.Context(), w, h, []assistant.Message{{Role: assistant.RoleUser, Text: "deploy it"}}, nil, nil, 4) {
			t.Error("the test server's writer cannot flush")
		}
	}))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("content type %q", ct)
	}
	// X-Accel-Buffering is what stops nginx holding the whole reply; without it
	// the streaming is real and invisible.
	if res.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("nothing tells a buffering proxy not to buffer")
	}

	lines := bufio.NewScanner(res.Body)
	read := func() map[string]any {
		if !lines.Scan() {
			t.Fatalf("stream ended early: %v", lines.Err())
		}
		var v map[string]any
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatalf("line %q: %v", lines.Text(), err)
		}
		return v
	}

	// The first line arrives before the provider has said anything at all,
	// which is the whole point: the model is still thinking and the connection
	// is already alive.
	if got := read()["type"]; got != "start" {
		t.Fatalf("first line is %v, want start", got)
	}
	// And it keeps arriving. The provider is still blocked here.
	if got := read()["type"]; got != "ping" {
		t.Fatalf("second line is %v, want a heartbeat while the provider works", got)
	}

	close(h.release)
	var sawTurn bool
	for {
		v := read()
		if v["type"] == "ping" {
			continue
		}
		if v["type"] == "turn" {
			sawTurn = true
			continue
		}
		if v["type"] != "done" {
			t.Fatalf("ended with %v", v)
		}
		if !sawTurn {
			t.Error("the answer was never reported turn by turn, only at the end")
		}
		if v["reply"] != "deployed" {
			t.Errorf("reply %v", v["reply"])
		}
		return
	}
}

// A provider that breaks mid-run cannot be reported as a status code: the
// header went out with the first line. It travels in the stream instead.
func TestAssistantStreamCarriesAFailureInTheStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := &held{turns: []assistant.Message{{Role: assistant.RoleAssistant, Calls: []assistant.ToolCall{{ID: "a", Name: "deploy_app"}}}}}
		exec := func(context.Context, assistant.ToolCall) (string, error) { return "", context.Canceled }
		streamAssistantRun(r.Context(), w, p, []assistant.Message{{Role: assistant.RoleUser, Text: "go"}}, nil, exec, 1)
	}))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status %d: the header is already written by the time a run can fail", res.StatusCode)
	}
	var last map[string]any
	lines := bufio.NewScanner(res.Body)
	for lines.Scan() {
		var v map[string]any
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatalf("line %q: %v", lines.Text(), err)
		}
		last = v
	}
	if last["type"] != "error" && last["type"] != "done" {
		t.Fatalf("stream ended on %v, so the client cannot tell finished from cut off", last)
	}
}
