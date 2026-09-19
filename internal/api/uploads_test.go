package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/assistant"
	"github.com/isletdev/islet/internal/uploads"
)

// A refused upload has to say which file and why, in a status the panel can
// tell apart. "Upload failed" for all three is the version that generates the
// support question.
func TestARefusedUploadSaysWhichFileAndWhy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"payload.zip", &uploads.Infected{Signature: "Eicar-Test-Signature"}, 422, "Eicar-Test-Signature"},
		{"holiday.mov", uploads.ErrTooBig, 413, "at most"},
		{"x.txt", errors.New("clamdscan could not check this file"), 502, "could not check"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		uploadErr(w, c.name, c.err)
		if w.Code != c.code {
			t.Errorf("%s: status %d, want %d", c.name, w.Code, c.code)
		}
		var body struct{ Error, Message string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(body.Message, c.name) {
			t.Errorf("%s: the message does not name the file: %q", c.name, body.Message)
		}
		if !strings.Contains(body.Message, c.want) {
			t.Errorf("%s: the message does not say why: %q", c.name, body.Message)
		}
	}
}

// A conversation started by dropping in files has no words to be named after.
func TestAConversationOfFilesIsNamedAfterThem(t *testing.T) {
	files := []assistant.Attachment{{Name: "logo.svg"}, {Name: "brand.zip"}, {Name: "hero.jpg"}}
	if got := chatTitle("make me a site", files); got != "make me a site" {
		t.Errorf("the words should win: %q", got)
	}
	if got := chatTitle("", files[:1]); got != "logo.svg" {
		t.Errorf("one file: %q", got)
	}
	if got := chatTitle("  ", files); got != "logo.svg and 2 more" {
		t.Errorf("three files: %q", got)
	}
	if got := chatTitle("", nil); got != "" {
		t.Errorf("nothing at all: %q", got)
	}
}

// Naming a media hostname adds the domain for it — that is the boring half of
// the task and doing the boring half is the point of the panel. But a hostname
// already serving an application is not a field to take over because it was
// typed on another page, and a refusal must leave the settings as they were
// rather than saving half of what was asked for.
//
// The route is exercised through the daemon in hack/e2e; this pins the shape of
// the decision, which is the part that would be wrong silently.
func TestAMediaHostnameNeverTakesAnApplicationsDomain(t *testing.T) {
	src, err := os.ReadFile("media.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	route := body[strings.Index(body, "func (s *Server) mediaRoute"):]
	route = route[:strings.Index(route, "\nfunc ")]

	if !strings.Contains(route, `d.TargetType != "panel"`) {
		t.Error("mediaRoute no longer checks what an existing domain points at")
	}
	if !strings.Contains(route, "already points at") {
		t.Error("the refusal no longer says what the host is being used for")
	}
	// Creating one that is already right has to be a no-op, or saving the same
	// settings twice writes a second domain the second time.
	if !strings.Contains(route, "if d.Enabled {\n\t\t\treturn nil\n\t\t}") {
		t.Error("mediaRoute no longer short-circuits on a domain that is already right")
	}

	// And the order: the refusable half runs first, so a refusal changes
	// nothing. Saving the hostname and then failing to route it would leave a
	// server configured for an address that answers somebody else's app.
	handler := body[strings.Index(body, "func (s *Server) handleMedia("):]
	handler = handler[:strings.Index(handler, "\nfunc ")]
	iRoute := strings.Index(handler, "s.mediaRoute(")
	iSave := strings.Index(handler, "s.media.SaveSettings(")
	if iRoute < 0 || iSave < 0 {
		t.Fatal("handleMedia no longer routes the host or no longer saves the settings")
	}
	if iRoute > iSave {
		t.Error("the settings are saved before the domain is claimed, so a refused save keeps half of itself")
	}
}
