package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	embedded "github.com/isletdev/islet/catalog"
)

func tarball(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestRefreshOverlay(t *testing.T) {
	good := tarball(map[string]string{
		"catalog-main/apps/testapp/islet.yaml":   "name: Test App\nslug: testapp\ncategory: other\ndescription: t\nservice: web\nport: 80\n",
		"catalog-main/apps/testapp/compose.yaml": "services:\n  web:\n    image: traefik/whoami:v1.10\n",
		"catalog-main/recipes/hello.yaml":        "name: Hello\nslug: hello\nsteps: []\n",
		"catalog-main/README.md":                 "ignored",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good.tar.gz":
			_, _ = w.Write(good)
		case "/empty.tar.gz":
			_, _ = w.Write(tarball(map[string]string{"x/README.md": "nothing"}))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	s := &Service{fsys: &layered{under: embedded.FS}, stacks: filepath.Join(dir, "stacks")}
	before, _ := s.List()
	if _, err := s.Refresh(context.Background(), srv.URL+"/empty.tar.gz"); err == nil {
		t.Fatal("empty archive should be refused")
	}
	if _, err := s.Refresh(context.Background(), srv.URL+"/missing.tar.gz"); err == nil {
		t.Fatal("404 should fail")
	}
	src, err := s.Refresh(context.Background(), srv.URL+"/good.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if src.Apps != 1 || src.Recipes != 1 {
		t.Fatalf("%+v", src)
	}
	after, _ := s.List()
	if len(after) != len(before)+1 {
		t.Fatalf("overlay not merged: %d vs %d", len(after), len(before))
	}
	if a, err := s.Get("testapp"); err != nil || a.Name != "Test App" || a.Compose == "" {
		t.Fatal("fetched app not readable", err)
	}
	if _, err := s.Get("postgres"); err != nil {
		t.Fatal("embedded app must still load")
	}
	// A fresh service on the same data dir picks the overlay up on start.
	s2 := &Service{fsys: &layered{under: embedded.FS}, stacks: filepath.Join(dir, "stacks")}
	s2.LoadOverlay()
	if _, err := s2.Get("testapp"); err != nil {
		t.Fatal("overlay not loaded on start")
	}
	if err := s2.ClearOverlay(); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Get("testapp"); err == nil {
		t.Fatal("overlay should be gone")
	}
}
