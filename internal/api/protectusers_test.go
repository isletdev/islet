package api

import (
	"testing"
	"time"
)

// The empty list is the old switch: protected, open to any signed-in account.
// Everything else is an exact membership test, because a rule that admitted
// somebody it does not name is the one failure mode that matters here.
func TestUserAllowed(t *testing.T) {
	cases := []struct {
		list, user string
		want       bool
	}{
		{"", "alice", true},
		{"   ", "alice", true},
		{"alice", "alice", true},
		{"alice,bob", "bob", true},
		{"alice,bob", "carol", false},
		{"alice", "Alice", true},   // the panel lower-cases on save; a session may not
		{"alice", "alic", false},   // no prefix match
		{"alice", "alicex", false}, // nor the other way round
		{"alice,", "alice", true},  // a trailing comma is not an empty member
	}
	for _, c := range cases {
		if got := userAllowed(c.list, c.user); got != c.want {
			t.Errorf("userAllowed(%q, %q) = %v, want %v", c.list, c.user, got, c.want)
		}
	}
}

// A refused browser asks again for every asset on the page, and a bot asks
// forever. One audit row per person per site per minute; the rest are the same
// event, and writing them all buries the one that matters.
func TestDenialsAreNotWrittenOncePerRequest(t *testing.T) {
	denied.Lock()
	denied.seen = nil
	denied.Unlock()

	n := 0
	for range 50 {
		if firstDenialIn(time.Minute, "bob|admin.example.com") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("wrote %d audit rows for one refusal, want 1", n)
	}
	// A different person, or a different site, is a different event.
	if !firstDenialIn(time.Minute, "alice|admin.example.com") || !firstDenialIn(time.Minute, "bob|other.example.com") {
		t.Fatal("a distinct refusal was swallowed")
	}
	// And the window does expire.
	if !firstDenialIn(0, "bob|admin.example.com") {
		t.Fatal("refusals are silenced forever, not for a window")
	}
}
