package hostown

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The claim file lives at a fixed path under /run, which a test must not write
// to — so these drive the same logic through a temporary path.
func withPath(t *testing.T, p string) {
	t.Helper()
	old := pathFor
	pathFor = func() string { return p }
	t.Cleanup(func() { pathFor = old })
}

// The first daemon to touch a host-level resource owns it, and says so.
func TestTheFirstClaimWins(t *testing.T) {
	withPath(t, filepath.Join(t.TempDir(), "owner"))
	if err := Claim("/var/lib/islet"); err != nil {
		t.Fatalf("a first claim should succeed: %v", err)
	}
	owner, ok := Owner()
	if !ok || owner != "/var/lib/islet" {
		t.Fatalf("owner is %q %v", owner, ok)
	}
	// The same daemon claiming again is not a conflict with itself.
	if err := Claim("/var/lib/islet"); err != nil {
		t.Errorf("a daemon lost its own claim: %v", err)
	}
}

// And the second one is refused, by name.
//
// This is the failure it exists for: a throwaway daemon started for a
// screenshot replaced the real proxy container with one pointed at its own
// config, and every site on the machine went down with nothing to say why.
func TestASecondDaemonIsRefused(t *testing.T) {
	withPath(t, filepath.Join(t.TempDir(), "owner"))
	if err := Claim("/var/lib/islet"); err != nil {
		t.Fatal(err)
	}
	err := Claim("/tmp/scratch/demo")
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("a second daemon was allowed in: %v", err)
	}
	msg := Refusal("reverse proxy").Error()
	if !contains(msg, "/var/lib/islet") {
		t.Errorf("the refusal does not name who holds it: %s", msg)
	}
	if !contains(msg, "reverse proxy") {
		t.Errorf("the refusal does not name what is held: %s", msg)
	}
}

// A relative data directory is the same machine as its absolute form, or a
// daemon started from a different working directory would lock itself out.
func TestTheSameDirectoryInTwoFormsIsOneOwner(t *testing.T) {
	dir := t.TempDir()
	withPath(t, filepath.Join(dir, "owner"))
	abs := filepath.Join(dir, "data")
	if err := os.MkdirAll(abs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Claim(abs); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := Claim("data"); err != nil {
		t.Errorf("the same directory relative was treated as another daemon: %v", err)
	}
}

// Nothing claimed means nothing to refuse: a machine with no /run entry has no
// owner, and a caller must not be blocked by bookkeeping.
func TestNoClaimIsNotARefusal(t *testing.T) {
	withPath(t, filepath.Join(t.TempDir(), "owner"))
	if _, ok := Owner(); ok {
		t.Error("an unclaimed machine reported an owner")
	}
	if err := Refusal("reverse proxy"); err != nil {
		t.Errorf("an unclaimed machine refused: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
