package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Traefik's access log is the one file in the data directory with no ceiling.
// Traefik does not rotate it — it expects a SIGUSR1 nobody was sending — and at
// roughly 300 bytes a line one busy site writes about ten gigabytes a year into
// the directory Islet's own disk alert watches.
func TestTheAccessLogIsRotatedOnlyWhenItIsLarge(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, nil, dir, "")
	path := filepath.Join(m.dir, "access.log")
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A log below the cap is left exactly as it is: rotating a small file
	// throws away recent traffic for no reason.
	if err := os.WriteFile(path, []byte("one line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.RotateAccessLog(context.Background())
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("a small log was rotated")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "one line\n" {
		t.Errorf("a small log was disturbed: %q %v", b, err)
	}

	// Over the cap it moves aside, so the next write starts a fresh file and
	// the total stays bounded at about twice the cap.
	big := strings.Repeat("x", maxAccessLog+1)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	m.RotateAccessLog(context.Background())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the live log should have been moved aside")
	}
	fi, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("the previous log was not kept: %v", err)
	}
	if fi.Size() != int64(len(big)) {
		t.Errorf("the previous log is %d bytes, want %d", fi.Size(), len(big))
	}
}

// And a missing log is not an error. The proxy may never have been installed.
func TestRotatingAnAccessLogThatIsNotThereDoesNothing(t *testing.T) {
	m := New(nil, nil, t.TempDir(), "")
	m.RotateAccessLog(context.Background()) // must not panic
}
