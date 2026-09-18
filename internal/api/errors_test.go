package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/proxy"
)

func body(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var e struct{ Error, Message string }
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("response is not an error object: %s", w.Body)
	}
	return e.Error, e.Message
}

// A name that is taken is not a malformed request. Thirteen of these answered
// 400, which tells a client to fix its request when the request was fine — and
// a client retrying a create has no way to tell "you sent nonsense" from "that
// one already exists".
func TestATakenNameIsAConflict(t *testing.T) {
	s := &Server{}
	for _, err := range []error{
		fmt.Errorf("an app with that name %w", deploy.ErrExists),
		fmt.Errorf("that host is %w", proxy.ErrExists),
	} {
		w := httptest.NewRecorder()
		s.failed(w, "invalid", err)
		if w.Code != http.StatusConflict {
			t.Errorf("%v answered %d, want 409", err, w.Code)
		}
		// The message is still the one written for a person.
		if _, msg := body(t, w); !strings.Contains(msg, "already") {
			t.Errorf("the message stopped being useful: %q", msg)
		}
	}
}

// A command that failed is not the client's fault and is not the client's to
// read. The text of a cmdrun error is the command line plus up to 400 bytes of
// its stderr: credentials are redacted, paths and unit names and whatever the
// tool printed are not.
func TestAFailedCommandIsNotReportedAsABadRequest(t *testing.T) {
	s := &Server{}
	inner := &cmdrun.Error{
		Cmd:    "docker exec islet-db-1 psql -h 10.0.0.5 -f /var/lib/islet/dumps/shop.sql",
		Result: cmdrun.Result{ExitCode: 1, Stderr: "could not connect to /var/run/postgresql/.s.PGSQL.5432"},
	}
	w := httptest.NewRecorder()
	s.failed(w, "db", fmt.Errorf("restoring the dump: %w", inner))

	if w.Code != http.StatusBadGateway {
		t.Errorf("a failed command answered %d, want 502", w.Code)
	}
	_, msg := body(t, w)
	for _, leaked := range []string{"/var/lib/islet", "islet-db-1", "10.0.0.5", "postgresql"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("the response carries %q from the command: %q", leaked, msg)
		}
	}
}

// And an ordinary validation error keeps its message, because those are the
// good part. "give it a name you will recognise later" is worth more than any
// status code in this file.
func TestAValidationErrorKeepsItsWords(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.failed(w, "invalid", errors.New("give it a name you will recognise later"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("answered %d, want 400", w.Code)
	}
	if kind, msg := body(t, w); kind != "invalid" || msg != "give it a name you will recognise later" {
		t.Errorf("got %q / %q", kind, msg)
	}
}

// A decoder error names the Go struct field it was filling. Seventy-four decode
// sites passed that through, so anybody sending a wrong type learned the
// daemon's internal field names.
func TestABadBodyDoesNotDescribeTheDaemonsStructs(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.badJSON(w, errors.New("json: cannot unmarshal number into Go struct field .Role of type string"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("answered %d, want 400", w.Code)
	}
	_, msg := body(t, w)
	for _, leaked := range []string{"Go struct", ".Role", "unmarshal"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("the response carries %q: %q", leaked, msg)
		}
	}
}
