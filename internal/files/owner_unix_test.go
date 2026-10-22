//go:build !windows

package files

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func ids(t *testing.T, p string) (uint32, uint32) {
	t.Helper()
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	return st.Uid, st.Gid
}

// The daemon is root. A folder it makes in somebody's project has to be theirs,
// open to them, and keep a shared folder's group — and so does a file it saves.
func TestCreatedPathsBelongToTheFolderTheyAreMadeIn(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only root creates something another user should own")
	}
	old := syscallUmask(0o027)
	defer syscallUmask(old)
	root := t.TempDir()
	proj := filepath.Join(root, "www")
	if err := os.Mkdir(proj, 0o775); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(proj, 0o775)
	if err := os.Chown(proj, 1234, 5678); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(root, "data"))
	deep := filepath.Join(proj, "trader", "src")
	if err := s.Mkdir(deep); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(proj, "trader"), deep} {
		if u, g := ids(t, d); u != 1234 || g != 5678 {
			t.Errorf("%s is %d:%d, want 1234:5678", d, u, g)
		}
		if info, _ := os.Stat(d); info.Mode().Perm() != 0o775 {
			t.Errorf("%s is %v, want 0775 under a group-writable folder", d, info.Mode().Perm())
		}
	}
	// The folder it started from is left exactly as it was.
	if u, _ := ids(t, root); u != 0 {
		t.Errorf("the existing parent changed hands")
	}

	f := filepath.Join(deep, "bot.py")
	if err := s.Write(f, []byte("print(1)\n")); err != nil {
		t.Fatal(err)
	}
	if u, g := ids(t, f); u != 1234 || g != 5678 {
		t.Errorf("a new file is %d:%d, want its folder's 1234:5678", u, g)
	}
	// Saving somebody else's file keeps it theirs.
	if err := os.Chown(f, 4321, 8765); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(f, []byte("print(2)\n")); err != nil {
		t.Fatal(err)
	}
	if u, g := ids(t, f); u != 4321 || g != 8765 {
		t.Errorf("saving changed the owner to %d:%d", u, g)
	}
	tf := filepath.Join(deep, "empty")
	if err := s.Touch(tf); err != nil {
		t.Fatal(err)
	}
	if u, _ := ids(t, tf); u != 1234 {
		t.Errorf("a touched file is owned by %d", u)
	}
}

func TestChownRecursiveChangesLinksNotTargets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown to another user needs root")
	}
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "proj")
	_ = os.MkdirAll(filepath.Join(dir, "a"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a", "f"), nil, 0o644)
	_ = os.Symlink(outside, filepath.Join(dir, "link"))
	if err := Chown(dir, "1234", "5678", true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, filepath.Join(dir, "a"), filepath.Join(dir, "a", "f"), filepath.Join(dir, "link")} {
		if u, g := ids(t, p); u != 1234 || g != 5678 {
			t.Errorf("%s is %d:%d", p, u, g)
		}
	}
	if u, _ := ids(t, outside); u != 0 {
		t.Errorf("a link handed over its target")
	}
	if err := Chown(filepath.Join(dir, "a"), "0", "", false); err != nil {
		t.Fatal(err)
	}
	if u, _ := ids(t, filepath.Join(dir, "a", "f")); u != 1234 {
		t.Errorf("a plain chown went inside the folder")
	}
}

func syscallUmask(m int) int { return syscall.Umask(m) }
