package files

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadTrashRestore(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "data"))
	p := filepath.Join(dir, "notes.txt")
	if err := s.Write(p, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	b, err := s.Read(p)
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("read = %q %v", b, err)
	}
	if err := s.Chmod(p, "0600", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(p, []byte("hello again\n")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 && os.PathSeparator != '\\' {
		t.Fatalf("mode not preserved: %v", info.Mode())
	}
	entries, err := s.List(dir)
	if err != nil || len(entries) != 1 || entries[0].Name != "notes.txt" {
		t.Fatalf("list = %v %v", entries, err)
	}
	it, err := s.Trash(p, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file still exists after trash")
	}
	list, _ := s.TrashList()
	if len(list) != 1 || list[0].ID != it.ID {
		t.Fatalf("trash list = %+v", list)
	}
	if _, err := s.Restore(it.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hello again\n" {
		t.Fatalf("restored content = %q", b)
	}
}

func TestArchiveExtractAndZipSlip(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "data"))
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("A"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("B"), 0o644)
	zipPath := filepath.Join(dir, "out.zip")
	if err := s.Archive([]string{src}, zipPath); err != nil {
		t.Fatal(err)
	}
	tgz := filepath.Join(dir, "out.tar.gz")
	if err := s.Archive([]string{src}, tgz); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{zipPath, tgz} {
		out := filepath.Join(dir, "x-"+filepath.Base(a))
		if err := s.Extract(a, out); err != nil {
			t.Fatalf("extract %s: %v", a, err)
		}
		if b, err := os.ReadFile(filepath.Join(out, "src", "sub", "b.txt")); err != nil || string(b) != "B" {
			t.Fatalf("extracted content wrong for %s: %q %v", a, b, err)
		}
	}
	// A zip whose entry escapes must be refused.
	var buf bytes.Buffer
	if err := Zip(&buf, []string{src}); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(dir, "evil.zip")
	_ = os.WriteFile(evil, bytes.Replace(buf.Bytes(), []byte("src/a.txt"), []byte("../a.txt"), 1), 0o644)
	if err := s.Extract(evil, filepath.Join(dir, "evil-out")); err == nil {
		t.Fatal("zip-slip entry accepted")
	}
}

func TestSearchAndUsage(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "data"))
	_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("port: 8080\nname: islet\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "other.txt"), []byte("nothing here"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hits, err := s.Search(ctx, dir, "config", false, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("name search = %v %v", hits, err)
	}
	hits, err = s.Search(ctx, dir, "islet", true, 10)
	if err != nil || len(hits) != 1 || hits[0].Line != 2 {
		t.Fatalf("content search = %v %v", hits, err)
	}
	u, err := s.DiskUsage(ctx, dir)
	if err != nil || u.Total != int64(len("port: 8080\nname: islet\n")+len("nothing here")) {
		t.Fatalf("usage = %+v %v", u, err)
	}
	if !IsProtected("/etc/shadow") || IsProtected("/home/x") {
		t.Fatal("protected paths wrong")
	}
}
