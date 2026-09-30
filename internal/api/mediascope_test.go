package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/media"
)

// Every scope a service route demands has to be one a key can actually carry.
//
// A route that asks for a scope nobody can hold is not a locked door, it is a
// door into a wall: the handler is there, the route is registered, and every
// request is refused with "this key may not do that" no matter what the key
// says. Transcoding shipped that way for about an hour — it asked for "write",
// and the four scopes are upload, read, delete and sign.
func TestEverySvcScopeIsARealOne(t *testing.T) {
	src, err := os.ReadFile("mediasvc.go")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, sc := range media.Scopes {
		known[sc] = true
	}
	re := regexp.MustCompile(`s\.svcKey\(w, r, "([a-z]+)"\)`)
	found := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		found++
		if !known[m[1]] {
			t.Errorf("a service route asks for the scope %q, which no key can hold (the scopes are %s)",
				m[1], strings.Join(media.Scopes, ", "))
		}
	}
	if found < 4 {
		t.Fatalf("only found %d scope checks; the pattern has stopped matching", found)
	}
}
