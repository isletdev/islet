package files

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reader that stops partway, the way a cancelled upload does.
type cutOff struct{ left int }

func (c *cutOff) Read(p []byte) (int, error) {
	if c.left == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := min(len(p), c.left)
	for i := range p[:n] {
		p[i] = 'x'
	}
	c.left -= n
	return n, nil
}

func TestReceiveLeavesNothingWhenTheUploadStops(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "data"))
	if _, err := s.Receive(dir, "video.mp4", &cutOff{left: 1 << 20}, false); err == nil {
		t.Fatal("a cut-off upload was accepted")
	}
	if names, _ := os.ReadDir(dir); len(names) != 0 {
		t.Fatalf("a cut-off upload left %s behind", names[0].Name())
	}
	// The retry is not blocked by a half file under the real name.
	p, err := s.Receive(dir, "video.mp4", strings.NewReader("whole"), false)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "whole" {
		t.Fatalf("saved %q", b)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o644 && os.PathSeparator != '\\' {
		t.Errorf("mode %v, want 0644", info.Mode().Perm())
	}
	if _, err := s.Receive(dir, "video.mp4", strings.NewReader("again"), false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a second upload of one name: %v, want ErrExist", err)
	}
	_ = os.Chmod(p, 0o600)
	if _, err := s.Receive(dir, "video.mp4", strings.NewReader("replaced"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "replaced" {
		t.Fatalf("replace saved %q", b)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 && os.PathSeparator != '\\' {
		t.Errorf("replacing changed the mode to %v", info.Mode().Perm())
	}
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	if _, err := s.Receive(dir, "sub", strings.NewReader("x"), true); err == nil {
		t.Error("an upload replaced a folder")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "islet-upload") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
}
