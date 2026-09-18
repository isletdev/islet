package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
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
