package api

import (
	"os"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/assistant"
)

// Both ways a run can end short have to say so, and there are exactly two: the
// person pressed Stop, or it failed. A branch added later that forgets to mark
// the turn puts the Continue button back where it does not belong — under a
// finished answer — which is the fault this replaced.
func TestEveryEndingShortOfDoneMarksTheTurn(t *testing.T) {
	src, err := os.ReadFile("assistant.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "out, err := assistant.RunStream(")
	if i < 0 {
		t.Fatal("the run no longer goes through RunStream")
	}
	tail := body[i:]
	end := strings.Index(tail, "\n\t}()")
	if end < 0 {
		t.Fatal("could not find the end of the run's switch")
	}
	sw := tail[:end]

	if !strings.Contains(sw[:strings.Index(sw, "rn.finish(\"cancelled\")")], "s.endedPartway(") {
		t.Error("a stopped run no longer marks the turn it stopped in the middle of")
	}
	failed := sw[strings.Index(sw, "rn.finish(\"cancelled\")"):strings.Index(sw, "rn.finish(\"error\")")]
	if !strings.Contains(failed, "s.endedPartway(") {
		t.Error("a failed run no longer marks the turn it failed in the middle of")
	}
	// And the ending that is not short of done leaves the turn alone, or every
	// answer is marked and nothing has changed.
	done := sw[strings.Index(sw, "rn.finish(\"error\")"):]
	if strings.Contains(done, "s.endedPartway(") {
		t.Error("a run that finished marks its turn as unfinished")
	}
}

// The mark has to reach both readers: the tab watching the run, which is handed
// `out` directly, and the row a tab opening the conversation tomorrow will read
// instead. Marking one and not the other is a button that appears until you
// reload, or only after you do.
func TestTheMarkReachesTheOpenTabAndTheStoredRow(t *testing.T) {
	src, err := os.ReadFile("assistant.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	fn := body[strings.Index(body, "func (s *Server) endedPartway("):]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !strings.Contains(fn, "out[n-1].Partial = true") {
		t.Error("the payload the open tab reads is no longer marked")
	}
	if !strings.Contains(fn, "s.chats.MarkLastPartial(") {
		t.Error("the stored row is no longer marked, so the button goes away on reload")
	}
	if !strings.Contains(fn, "assistant.RoleAssistant") {
		t.Error("endedPartway no longer checks that the last turn is an answer")
	}
}

// Nothing marks a turn on the way in. A turn that arrived unfinished would be
// one the model wrote that way, which is not a thing that happens.
func TestATurnIsNotBornUnfinished(t *testing.T) {
	var m assistant.Message
	if m.Partial {
		t.Error("the zero value of a turn is unfinished")
	}
}
