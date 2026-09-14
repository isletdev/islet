package security

import (
	"strings"
	"testing"
)

// The snippet and the string everything looks for have to stay together: if
// the marker is not actually in the text, the rules are appended on every run
// and FirewallStatus reports them missing forever.
func TestDockerMarkerIsInTheSnippet(t *testing.T) {
	if !strings.Contains(dockerRules, dockerMarker) {
		t.Fatalf("dockerRules does not contain dockerMarker %q", dockerMarker)
	}
}

func TestWithDockerRules(t *testing.T) {
	stock := []byte("*filter\n:ufw-after-input - [0:0]\nCOMMIT\n")

	next, added := withDockerRules(stock)
	if !added {
		t.Fatal("a stock after.rules should gain the snippet")
	}
	if !strings.Contains(string(next), dockerMarker) {
		t.Fatal("the snippet was not appended")
	}
	if !strings.HasPrefix(string(next), string(stock)) {
		t.Fatal("the existing rules must be kept, not replaced")
	}

	// Running the fix twice must not stack the chains up twice.
	again, addedAgain := withDockerRules(next)
	if addedAgain {
		t.Fatal("a file that already carries the snippet should be left alone")
	}
	if strings.Count(string(again), dockerMarker) != 1 {
		t.Fatalf("marker appears %d times, want 1", strings.Count(string(again), dockerMarker))
	}

	// The caller must not be able to mutate the input through the result.
	if &stock[0] == &next[0] {
		t.Fatal("withDockerRules aliased its input")
	}
}

// The bug this guards: EnableFirewall wrote the snippet and then ran
// `ufw --force reset`, which restores after.rules from the package. Written in
// that order the snippet is always discarded, and the fix still reports
// success. Reset is modelled here as "the file goes back to stock".
func TestSnippetSurvivesOnlyWhenWrittenAfterReset(t *testing.T) {
	stock := []byte("*filter\nCOMMIT\n")
	reset := func([]byte) []byte { return append([]byte{}, stock...) }

	// The old order: write, then reset.
	written, _ := withDockerRules(stock)
	afterWrongOrder := reset(written)
	if strings.Contains(string(afterWrongOrder), dockerMarker) {
		t.Fatal("test is wrong: a reset must discard the snippet")
	}

	// The order shipped now: reset, then write.
	afterRightOrder, added := withDockerRules(reset(stock))
	if !added || !strings.Contains(string(afterRightOrder), dockerMarker) {
		t.Fatal("writing after the reset must leave the snippet in place")
	}
}
