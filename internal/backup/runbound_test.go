package backup

import (
	"strings"
	"testing"
)

// The output of a command nobody reads until it fails has to be bounded.
//
// This one is the tail of restic's stderr during a run that can last hours, at
// a destination that may be retrying the whole time, and it was collected into
// a strings.Builder that only grows. It is also the one command in the tree
// that does not go through cmdrun, so it did not inherit cmdrun's limit.
func TestTheBackupStderrBufferIsBounded(t *testing.T) {
	c := &capped{max: 64}
	chunk := strings.Repeat("x", 100)
	for i := 0; i < 1000; i++ {
		n, err := c.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("a bounded writer must not fail the producer: %v", err)
		}
		// The whole write is reported even after the cap: the producer is not
		// at fault, and a short write would turn a truncated log into a broken
		// command.
		if n != len(chunk) {
			t.Fatalf("write %d reported %d of %d bytes", i, n, len(chunk))
		}
	}
	if got := len(c.String()); got != 64 {
		t.Errorf("kept %d bytes, want the cap of 64", got)
	}
}

// And what it keeps is what was written first, which is where a failure that
// happens at the start of a run is described.
func TestTheBoundedBufferKeepsWhatItSaw(t *testing.T) {
	c := &capped{max: 10}
	_, _ = c.Write([]byte("repository"))
	_, _ = c.Write([]byte(" is locked"))
	if c.String() != "repository" {
		t.Errorf("kept %q", c.String())
	}
}
