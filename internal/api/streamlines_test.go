package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamLines drains the request body before it writes, which is right for a
// request read off a socket — net/http always gives a handler a non-nil Body.
// A request assembled in process does not have to, and the tools assemble one:
// the diagnostics tool reached this and took the daemon's handler down with a
// nil dereference, answering "internal error" to a call that was perfectly
// valid.
func TestStreamLinesSurvivesARequestWithNoBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics?host=x&tool=ping", nil)
	r.Body = nil
	w := httptest.NewRecorder()
	rc := io.NopCloser(strings.NewReader("first\nsecond\n"))
	streamLines(w, r, rc, func() error { return nil })

	body := w.Body.String()
	for _, want := range []string{"data: \"first\"", "data: \"second\"", "event: end", `data: {"ok":true}`} {
		if !strings.Contains(body, want) {
			t.Errorf("stream is missing %q:\n%s", want, body)
		}
	}
}

// And the failure of the command still reaches the client as the end event,
// which is the only place it can go once the header is out.
func TestStreamLinesReportsAFailureInTheEndEvent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	streamLines(w, r, io.NopCloser(strings.NewReader("out\n")), func() error { return errors.New("exit status 2") })
	end := lastEvent(t, w.Body.String())
	if ok, _ := end["ok"].(bool); ok {
		t.Errorf("a failed command ended ok:\n%s", w.Body.String())
	}
	if msg, _ := end["message"].(string); !strings.Contains(msg, "exit status 2") {
		t.Errorf("the failure never reached the client:\n%s", w.Body.String())
	}
}

// Every event a stream writes has to be JSON, including the last one.
//
// It was not: the end event was written with %q, which is Go quoting. Go
// escapes an ESC byte as \x1b and JSON has no such escape, so a command whose
// stderr carried colour — docker compose and every build tool, by default —
// produced a payload the panel's JSON.parse threw on, inside the loop reading
// the stream. The user was told the connection dropped, for a command that had
// finished and failed, and the error itself was never shown.
func TestTheEndEventIsJSONEvenWhenTheErrorCarriesANSIColour(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	boom := errors.New("docker build: exit 1: \x1b[31mfailed to solve\x1b[0m")
	streamLines(w, r, io.NopCloser(strings.NewReader("")), func() error { return boom })

	end := lastEvent(t, w.Body.String())
	if ok, _ := end["ok"].(bool); ok {
		t.Fatalf("a failed build ended ok:\n%s", w.Body.String())
	}
	if msg, _ := end["message"].(string); !strings.Contains(msg, "failed to solve") {
		t.Errorf("the error text did not survive the round trip: %q", msg)
	}
}

// lastEvent parses the data of the final SSE event, the way a client does.
// Parsing rather than substring-matching is the point: a payload that is not
// JSON fails here, which is the bug this file now guards.
func lastEvent(t *testing.T, body string) map[string]any {
	t.Helper()
	var data string
	for _, line := range strings.Split(body, "\n") {
		if d, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "data: "); ok {
			data = d
		}
	}
	if data == "" {
		t.Fatalf("no data line in:\n%s", body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(data), &out); err != nil {
		t.Fatalf("the end event is not JSON: %v\ndata: %s", err, data)
	}
	return out
}
