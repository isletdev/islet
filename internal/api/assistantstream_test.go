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

// held is a provider whose turn waits to be released, standing in for the
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

// startRun drives a provider through a run the way the handler does, with a
// context that does not belong to any request.
func startRun(t *testing.T, p assistant.Provider, exec assistant.Executor) (*runs, *run) {
	t.Helper()
	rs := newRuns()
	ctx, cancel := context.WithCancel(context.Background())
	rn := rs.start("alice", "do the thing", cancel)
	rn.add("start", map[string]any{"runId": rn.ID, "tools": 0})
	go func() {
		defer cancel()
		out, err := assistant.RunStream(ctx, p, "", []assistant.Message{{Role: assistant.RoleUser, Text: "go"}}, nil, exec, 4, rn.observer())
		if err != nil {
			rn.add("error", map[string]any{"message": err.Error(), "messages": out})
			rn.finish("error")
			return
		}
		rn.add("done", map[string]any{"messages": out, "reply": assistant.Text(out)})
		rn.finish("done")
	}()
	return rs, rn
}

func readEvent(t *testing.T, lines *bufio.Scanner) map[string]any {
	t.Helper()
	for lines.Scan() {
		var v map[string]any
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatalf("line %q: %v", lines.Text(), err)
		}
		if v["type"] == "ping" {
			continue
		}
		return v
	}
	t.Fatalf("stream ended early: %v", lines.Err())
	return nil
}

// The bug this is for: a phone that locks its screen, an app sent to the
// background or a tab swiped away closes the connection, and the run went with
// it — stopping halfway through a deploy with nothing kept. The run belongs to
// the daemon now, so the connection closing ends only the watching.
func TestAClientGoingAwayDoesNotStopTheRun(t *testing.T) {
	h := &held{release: make(chan struct{}), turns: []assistant.Message{
		{Role: assistant.RoleAssistant, Text: "deployed"},
	}}
	_, rn := startRun(t, h, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamRun(r.Context(), w, rn, 0)
	}))
	t.Cleanup(srv.Close)

	// Watch, then vanish mid-run, the way a phone does.
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(res.Body)
	if got := readEvent(t, lines)["type"]; got != "start" {
		t.Fatalf("first event is %v, want start", got)
	}
	res.Body.Close()

	// The provider answers after nobody is listening at all.
	time.Sleep(20 * time.Millisecond)
	close(h.release)

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, _ := rn.state()
		if status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run ended as %q; work started from a phone must survive the phone", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// And the answer is still there to be collected.
	events, _, _ := rn.since(0)
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("last event is %q", last.Type)
	}
	var payload struct {
		Reply string `json:"reply"`
	}
	_ = json.Unmarshal(last.Data, &payload)
	if payload.Reply != "deployed" {
		t.Errorf("reply %q was not kept for whoever comes back", payload.Reply)
	}
}

// Coming back is the other half. The client says what it already has, and gets
// what it missed and then the rest — nothing repeated, nothing dropped.
func TestReattachingResumesFromWhereTheClientGotTo(t *testing.T) {
	h := &held{release: make(chan struct{}), turns: []assistant.Message{
		{Role: assistant.RoleAssistant, Text: "back again"},
	}}
	_, rn := startRun(t, h, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		from := 0
		if v := r.URL.Query().Get("from"); v == "1" {
			from = 1
		}
		streamRun(r.Context(), w, rn, from)
	}))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	first := readEvent(t, bufio.NewScanner(res.Body))
	if first["type"] != "start" {
		t.Fatalf("first event %v", first)
	}
	res.Body.Close()
	close(h.release)

	// Reattach having seen event 0, after the run has finished.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if st, _ := rn.state(); st != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	res2, err := http.Get(srv.URL + "?from=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	lines := bufio.NewScanner(res2.Body)
	var types []string
	for lines.Scan() {
		var v map[string]any
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v["type"] == "ping" {
			continue
		}
		if int(v["seq"].(float64)) == 0 {
			t.Error("event 0 was replayed to a client that already had it")
		}
		types = append(types, v["type"].(string))
	}
	if len(types) == 0 || types[len(types)-1] != "done" {
		t.Fatalf("reattaching gave %v, want it to end on done", types)
	}
}

// What the tools are doing is the thing somebody watching actually wants. A
// screen that says "Working…" for four minutes is indistinguishable from one
// that has hung.
func TestTheStreamSaysWhichToolIsRunning(t *testing.T) {
	h := &held{turns: []assistant.Message{
		{Role: assistant.RoleAssistant, Calls: []assistant.ToolCall{{ID: "a", Name: "list_domains", Input: map[string]any{"limit": 10}}}},
		{Role: assistant.RoleAssistant, Text: "eleven"},
	}}
	_, rn := startRun(t, h, func(context.Context, assistant.ToolCall) (string, error) { return "[]", nil })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamRun(r.Context(), w, rn, 0)
	}))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := bufio.NewScanner(res.Body)
	seen := map[string]map[string]any{}
	for {
		v := readEvent(t, lines)
		seen[v["type"].(string)] = v
		if v["type"] == "done" {
			break
		}
	}
	tool, ok := seen["tool"]
	if !ok {
		t.Fatal("the stream never said which tool was being called")
	}
	if tool["name"] != "list_domains" {
		t.Errorf("tool event names %v", tool["name"])
	}
	end, ok := seen["tool_done"]
	if !ok {
		t.Fatal("the stream never said the tool had finished")
	}
	if end["ok"] != true {
		t.Errorf("tool_done reports %v", end["ok"])
	}
	if _, has := end["ms"]; !has {
		t.Error("tool_done does not say how long it took")
	}
}

// A run that is idle still has to send something: the proxy in front counts
// silence, and one tool call can be a build that takes minutes.
func TestAnIdleRunKeepsTheConnectionFed(t *testing.T) {
	old := assistantHeartbeat
	assistantHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() { assistantHeartbeat = old })

	h := &held{release: make(chan struct{})}
	_, rn := startRun(t, h, nil)
	t.Cleanup(func() { close(h.release) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamRun(r.Context(), w, rn, 0)
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
	if res.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("nothing tells a buffering proxy not to buffer")
	}
	lines := bufio.NewScanner(res.Body)
	var pinged bool
	for i := 0; i < 3 && lines.Scan(); i++ {
		var v map[string]any
		_ = json.Unmarshal(lines.Bytes(), &v)
		if v["type"] == "ping" {
			pinged = true
			break
		}
	}
	if !pinged {
		t.Error("an idle run sent nothing, so a proxy would time the request out")
	}
}

// A panel tab open across an update is still running the build that read the
// whole reply with JSON.parse, and newline-delimited JSON fails it on the first
// line break — "Unexpected non-whitespace character after JSON at position 28",
// which tells the person nothing and looks like a broken assistant. That client
// says what it can read, so it is taken at its word.
func TestTheShapeOfTheReplyFollowsAccept(t *testing.T) {
	cases := []struct {
		accept string
		stream bool
		why    string
	}{
		{"", true, "a client that says nothing gets the stream, since the timeout is its problem too"},
		{"*/*", true, "curl and every script default to this"},
		{"application/x-ndjson, application/json", true, "the panel after the update"},
		{"application/json", false, "the panel before it, which parses the whole body at once"},
		{"application/json, text/plain, */*", false, "the common library default, and those parse the body at once as well"},
		{"text/event-stream", true, "asked for a stream of some kind"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/chat", nil)
		if c.accept != "" {
			r.Header.Set("Accept", c.accept)
		}
		if got := acceptsStream(r); got != c.stream {
			t.Errorf("Accept: %q streams = %v, want %v — %s", c.accept, got, c.stream, c.why)
		}
	}
}

// Only finished runs are dropped when the registry fills. The one still working
// is the one somebody is waiting on, and evicting it would lose the work this
// whole mechanism exists to keep.
func TestAWorkingRunIsNeverEvicted(t *testing.T) {
	rs := newRuns()
	keep := rs.start("alice", "the long one", func() {})
	for i := 0; i < maxKeptRuns+5; i++ {
		r := rs.start("alice", "quick", func() {})
		r.finish("done")
	}
	if rs.get(keep.ID) == nil {
		t.Fatal("the run still working was evicted")
	}
	if n := len(rs.list("alice")); n > maxKeptRuns+1 {
		t.Errorf("registry kept %d runs, which is unbounded in practice", n)
	}
}
