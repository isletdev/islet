package api

import (
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
	for _, want := range []string{"data: \"first\"", "data: \"second\"", "event: end"} {
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
	if !strings.Contains(w.Body.String(), "exit status 2") {
		t.Errorf("the failure never reached the client:\n%s", w.Body.String())
	}
}
