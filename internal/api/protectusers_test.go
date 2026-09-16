package api

import "testing"

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
