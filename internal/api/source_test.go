package api

import (
	"os"
	"testing"
)

// readSource is for the tests in this package that pin a decision by reading
// the code that makes it. They exist where the decision is a line that would
// otherwise be deleted in a refactor with nothing to notice.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("cannot read %s: %v", name, err)
	}
	return string(b)
}
