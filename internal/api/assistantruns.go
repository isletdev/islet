package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/assistant"
)

// A run is a conversation the daemon is having on somebody's behalf, and it
// belongs to the daemon rather than to the request that started it.
//
// The reason is a phone. A screen that locks, an app that goes to the
// background or a tab that is swiped away all close the connection, and while
// the loop was bound to the request that meant the work stopped — mid-deploy,
// with no record of how far it got. A run now keeps going, keeps its events,
// and is there to be picked up again when the phone comes back.
//
// Events are kept in memory. They are worth exactly as long as the daemon is
// up: an update restarts it, and a model call cannot be resumed across that in
// any case, so a run interrupted that way is reported as interrupted rather
// than pretended about.
type run struct {
	ID      string
	User    string
	ChatID  string // the conversation it belongs to, if any
	Ask     string // the question, for a list somebody is choosing from
	Started time.Time

	mu      sync.Mutex
	events  []runEvent
	ended   time.Time
	status  string // running | done | error | cancelled
	stopped bool
	changed chan struct{}
	cancel  context.CancelFunc
}

// runEvent is one line of the stream. Seq is what a client reattaching says it
// has already seen, so nothing is replayed twice and nothing is missed.
type runEvent struct {
	Seq  int             `json:"seq"`
	At   string          `json:"at"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"-"`
}

// MarshalJSON flattens the event so a client reads {"seq":3,"type":"tool",…}
// rather than a payload nested under a key.
func (e runEvent) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &out); err != nil {
			return nil, err
		}
	}
	out["seq"], out["type"], out["at"] = e.Seq, e.Type, e.At
	return json.Marshal(out)
}

func (r *run) add(kind string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte("{}")
	}
	r.mu.Lock()
	r.events = append(r.events, runEvent{Seq: len(r.events), At: time.Now().UTC().Format(time.RFC3339Nano), Type: kind, Data: b})
	// Everyone waiting is woken by the channel closing, and the next wait uses
	// a fresh one. A condition variable cannot be selected on alongside a
	// request being cancelled, which is what every reader here needs to do.
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
}

// since returns the events after seq, and a channel that closes when more
// arrive. Both under one lock, or a client can miss an event that lands
// between the read and the wait.
func (r *run) since(seq int) ([]runEvent, <-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runEvent
	if seq < len(r.events) {
		out = append(out, r.events[seq:]...)
	}
	return out, r.changed, r.status == "running"
}

func (r *run) finish(status string) {
	r.mu.Lock()
	r.status, r.ended = status, time.Now()
	r.mu.Unlock()
}

// stop cancels a run at somebody's request, and records that it was asked for
// so the end of the stream can say "stopped" rather than report an error.
func (r *run) stop() {
	r.mu.Lock()
	r.stopped = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *run) cancelled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

// wait blocks until the run ends and returns its transcript, for a client that
// cannot read a stream. ctx is the caller's, so it gives up when they do —
// without stopping the run.
func (r *run) wait(ctx context.Context) ([]assistant.Message, error) {
	for {
		r.mu.Lock()
		status, events, changed := r.status, r.events, r.changed
		r.mu.Unlock()
		if status != "running" {
			return finalOf(events)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// finalOf reads the transcript out of the last event, which is where both the
// answer and the failure carry it.
func finalOf(events []runEvent) ([]assistant.Message, error) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Type != "done" && e.Type != "error" {
			continue
		}
		var payload struct {
			Messages []assistant.Message `json:"messages"`
			Message  string              `json:"message"`
		}
		_ = json.Unmarshal(e.Data, &payload)
		if e.Type == "error" {
			return payload.Messages, errors.New(payload.Message)
		}
		return payload.Messages, nil
	}
	return nil, errors.New("the run ended without saying anything")
}

func (r *run) state() (status string, events int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, len(r.events)
}

// runs holds the recent ones. The cap is small on purpose: this is a single
// server's panel, the events are only useful while somebody might come back to
// them, and an unbounded map of transcripts is a memory leak with a nice name.
type runs struct {
	mu    sync.Mutex
	byID  map[string]*run
	order []string
}

const maxKeptRuns = 20

func newRuns() *runs { return &runs{byID: map[string]*run{}} }

func (rs *runs) start(user, chatID, ask string, cancel context.CancelFunc) *run {
	r := &run{
		ID: newRunID(), User: user, ChatID: chatID, Ask: ask, Started: time.Now(),
		status: "running", changed: make(chan struct{}), cancel: cancel,
	}
	rs.mu.Lock()
	rs.byID[r.ID] = r
	rs.order = append(rs.order, r.ID)
	// Only finished runs are dropped. A run still working is never evicted,
	// however old: it is the one somebody is waiting on.
	for len(rs.order) > maxKeptRuns {
		dropped := false
		for i, id := range rs.order {
			if old, ok := rs.byID[id]; ok {
				if st, _ := old.state(); st == "running" {
					continue
				}
			}
			delete(rs.byID, id)
			rs.order = append(rs.order[:i], rs.order[i+1:]...)
			dropped = true
			break
		}
		if !dropped {
			break
		}
	}
	rs.mu.Unlock()
	return r
}

// activeFor is the run working on a conversation, if one is.
//
// One at a time per conversation: two loops appending to the same transcript
// would interleave into something neither of them meant, and the person would
// be watching one of them at random. Different conversations run side by side,
// which is the point of having more than one.
func (rs *runs) activeFor(chatID string) *run {
	if chatID == "" {
		return nil
	}
	rs.mu.Lock()
	ids := append([]string(nil), rs.order...)
	byID := make(map[string]*run, len(rs.byID))
	for k, v := range rs.byID {
		byID[k] = v
	}
	rs.mu.Unlock()
	for i := len(ids) - 1; i >= 0; i-- {
		r, ok := byID[ids[i]]
		if !ok || r.ChatID != chatID {
			continue
		}
		if st, _ := r.state(); st == "running" {
			return r
		}
	}
	return nil
}

func (rs *runs) get(id string) *run {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.byID[id]
}

// list returns one person's runs, newest first.
func (rs *runs) list(user string) []*run {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]*run, 0, len(rs.order))
	for i := len(rs.order) - 1; i >= 0; i-- {
		if r, ok := rs.byID[rs.order[i]]; ok && r.User == user {
			out = append(out, r)
		}
	}
	return out
}

func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Time alone is enough to tell two runs apart on one server; the random
		// half only stops an id being guessable from when it started.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// observer wires the loop's progress into a run's event log.
func (r *run) observer() *assistant.Observer {
	return &assistant.Observer{
		Turn: func(m assistant.Message) { r.add("turn", map[string]any{"message": m}) },
		Text: func(t string) { r.add("text", map[string]any{"text": t}) },
		ToolStart: func(c assistant.ToolCall) {
			r.add("tool", map[string]any{"id": c.ID, "name": c.Name, "input": summariseInput(c.Input)})
		},
		ToolEnd: func(c assistant.ToolCall, res assistant.ToolResult, d time.Duration) {
			r.add("tool_done", map[string]any{
				"id": c.ID, "name": c.Name, "ms": d.Milliseconds(),
				"ok": !res.IsError, "preview": preview(res.Content),
			})
		},
	}
}

// summariseInput keeps a tool's arguments small enough to show. A create_stack
// call carries a whole Compose file, and nobody watching needs it on screen.
func summariseInput(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok && len(s) > 120 {
			out[k] = s[:120] + "…"
			continue
		}
		out[k] = v
	}
	return out
}

func preview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
