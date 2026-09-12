package catalog

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The catalog can be refreshed at runtime from a tarball (a GitHub archive
// of the catalog repository). Fetched templates overlay the embedded ones,
// so a fresh install works offline and a configured source brings newer
// apps and recipes without a daemon update.

// layered serves from the fetched directory first, then the embedded FS.
type layered struct {
	mu    sync.RWMutex
	over  fs.FS // may be nil
	under fs.FS
}

func (l *layered) Open(name string) (fs.File, error) {
	l.mu.RLock()
	over := l.over
	l.mu.RUnlock()
	if over != nil {
		if f, err := over.Open(name); err == nil {
			return f, nil
		}
	}
	return l.under.Open(name)
}

// ReadDir merges both layers so new apps appear next to embedded ones.
func (l *layered) ReadDir(name string) ([]fs.DirEntry, error) {
	l.mu.RLock()
	over := l.over
	l.mu.RUnlock()
	seen := map[string]bool{}
	var out []fs.DirEntry
	if over != nil {
		if ents, err := fs.ReadDir(over, name); err == nil {
			for _, e := range ents {
				seen[e.Name()] = true
				out = append(out, e)
			}
		}
	}
	ents, err := fs.ReadDir(l.under, name)
	if err != nil && out == nil {
		return nil, err
	}
	for _, e := range ents {
		if !seen[e.Name()] {
			out = append(out, e)
		}
	}
	return out, nil
}

func (l *layered) set(over fs.FS) {
	l.mu.Lock()
	l.over = over
	l.mu.Unlock()
}

// FS is the catalog filesystem (embedded plus any fetched overlay); other
// packages (recipes) read templates through it.
func (s *Service) FS() fs.FS { return s.fsys }

// Source is the configured tarball URL and the last refresh state.
type Source struct {
	URL         string `json:"url"`
	FetchedAt   string `json:"fetchedAt"`
	Apps        int    `json:"apps"`
	Recipes     int    `json:"recipes"`
	LastError   string `json:"lastError,omitempty"`
	OverlayPath string `json:"-"`
}

func (s *Service) overlayDir() string { return filepath.Join(filepath.Dir(s.stacks), "catalog") }

// LoadOverlay activates a previously fetched catalog on startup.
func (s *Service) LoadOverlay() {
	dir := s.overlayDir()
	if st, err := os.Stat(filepath.Join(dir, "apps")); err == nil && st.IsDir() {
		if l, ok := s.fsys.(*layered); ok {
			l.set(os.DirFS(dir))
		}
	}
}

// ClearOverlay removes the fetched catalog and goes back to embedded only.
func (s *Service) ClearOverlay() error {
	if l, ok := s.fsys.(*layered); ok {
		l.set(nil)
	}
	return os.RemoveAll(s.overlayDir())
}

// Refresh downloads url (a .tar.gz with apps/ and recipes/, optionally
// inside one top-level folder as GitHub archives are), validates it and
// swaps it in atomically.
func (s *Service) Refresh(ctx context.Context, url string) (Source, error) {
	src := Source{URL: url}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return src, errors.New("the catalog source must be an http(s) URL to a .tar.gz")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return src, err
	}
	req.Header.Set("User-Agent", "islet-catalog")
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return src, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return src, fmt.Errorf("download failed: %s", resp.Status)
	}
	tmp := s.overlayDir() + ".new"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return src, err
	}
	apps, recipes, err := extract(io.LimitReader(resp.Body, 64<<20), tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return src, err
	}
	if apps == 0 {
		os.RemoveAll(tmp)
		return src, errors.New("the archive holds no apps/<slug>/islet.yaml")
	}
	// Every app must parse, or the whole refresh is refused.
	probe := &Service{fsys: os.DirFS(tmp)}
	if list, err := probe.List(); err != nil || len(list) == 0 {
		os.RemoveAll(tmp)
		return src, errors.New("the archive's apps do not parse")
	}
	final := s.overlayDir()
	old := final + ".old"
	_ = os.RemoveAll(old)
	_ = os.Rename(final, old)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Rename(old, final)
		return src, err
	}
	_ = os.RemoveAll(old)
	if l, ok := s.fsys.(*layered); ok {
		l.set(os.DirFS(final))
	}
	src.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	src.Apps, src.Recipes = apps, recipes
	return src, nil
}

// extract writes apps/ and recipes/ from a tar.gz into dir, stripping one
// leading folder when present. Returns how many apps and recipes it saw.
func extract(r io.Reader, dir string) (apps, recipes int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, 0, errors.New("that is not a .tar.gz")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return apps, recipes, err
		}
		name := path.Clean(strings.ReplaceAll(h.Name, "\\", "/"))
		parts := strings.Split(name, "/")
		// GitHub archives: <repo>-<ref>/apps/...; plain tarballs: apps/...
		if len(parts) > 1 && parts[0] != "apps" && parts[0] != "recipes" {
			parts = parts[1:]
		}
		if len(parts) < 2 || (parts[0] != "apps" && parts[0] != "recipes") || strings.Contains(name, "..") {
			continue
		}
		rel := path.Join(parts...)
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(dst, 0o755)
		case tar.TypeReg:
			if h.Size > 1<<20 {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return apps, recipes, err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return apps, recipes, err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return apps, recipes, err
			}
			if parts[0] == "apps" && len(parts) == 3 && parts[2] == "islet.yaml" {
				apps++
			}
			if parts[0] == "recipes" && len(parts) == 2 && strings.HasSuffix(parts[1], ".yaml") {
				recipes++
			}
		}
	}
	return apps, recipes, nil
}
