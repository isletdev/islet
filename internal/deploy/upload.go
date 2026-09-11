package deploy

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// UploadMeta records what was uploaded for an app whose source is "upload".
type UploadMeta struct {
	Name  string `json:"name"` // zip file name or folder name
	By    string `json:"by"`
	At    string `json:"at"`
	Files int    `json:"files"`
}

const maxUploadBytes = 1 << 30 // 1 GB unpacked

func (s *Service) uploadDir(id string) string { return filepath.Join(s.dir, id, "upload") }

func readUploadMeta(dir string) (*UploadMeta, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".islet-upload.json"))
	if err != nil {
		return nil, err
	}
	var m UploadMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Upload replaces the app's uploaded source with the multipart body: a
// part named "zip" is extracted, parts named "file:<relative path>" are
// written as-is (a folder picked or dropped in the browser). A single
// top-level folder is flattened so the project root is the upload root.
func (s *Service) Upload(ctx context.Context, actor, id string, mr *multipart.Reader) (*UploadMeta, *Detection, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if a.Source != "upload" {
		return nil, nil, errors.New("this app deploys from " + a.Source + "; switch its source to upload first")
	}
	dir := s.uploadDir(id)
	tmp := dir + ".new"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, nil, err
	}
	meta := UploadMeta{By: actor, At: time.Now().UTC().Format(time.RFC3339)}
	var total int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			os.RemoveAll(tmp)
			return nil, nil, err
		}
		name := part.FormName()
		switch {
		case name == "zip":
			if meta.Name == "" {
				meta.Name = part.FileName()
			}
			n, files, err := extractZip(part, tmp, maxUploadBytes-total)
			total += n
			meta.Files += files
			if err != nil {
				os.RemoveAll(tmp)
				return nil, nil, err
			}
		case strings.HasPrefix(name, "file:"):
			rel := path.Clean(strings.ReplaceAll(strings.TrimPrefix(name, "file:"), "\\", "/"))
			if rel == "." || rel == "/" || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
				_, _ = io.Copy(io.Discard, part)
				continue
			}
			if meta.Name == "" {
				meta.Name = strings.SplitN(rel, "/", 2)[0]
			}
			dst := filepath.Join(tmp, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				os.RemoveAll(tmp)
				return nil, nil, err
			}
			f, err := os.Create(dst)
			if err != nil {
				os.RemoveAll(tmp)
				return nil, nil, err
			}
			n, err := io.Copy(f, io.LimitReader(part, maxUploadBytes-total+1))
			f.Close()
			total += n
			if err != nil || total > maxUploadBytes {
				os.RemoveAll(tmp)
				if total > maxUploadBytes {
					return nil, nil, errors.New("upload is larger than 1 GB")
				}
				return nil, nil, err
			}
			meta.Files++
		default:
			_, _ = io.Copy(io.Discard, part)
		}
	}
	if meta.Files == 0 {
		os.RemoveAll(tmp)
		return nil, nil, errors.New("nothing was uploaded: drop a folder or a .zip")
	}
	flatten(tmp)
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(tmp, ".islet-upload.json"), b, 0o644); err != nil {
		os.RemoveAll(tmp)
		return nil, nil, err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return nil, nil, err
	}
	d := Detect(filepath.Join(dir, a.RootDir))
	return &meta, &d, nil
}

// extractZip unpacks part into dir, refusing paths that leave dir.
func extractZip(part io.Reader, dir string, budget int64) (int64, int, error) {
	tf, err := os.CreateTemp(filepath.Dir(dir), "upload-*.zip")
	if err != nil {
		return 0, 0, err
	}
	defer os.Remove(tf.Name())
	size, err := io.Copy(tf, io.LimitReader(part, budget+1))
	tf.Close()
	if err != nil {
		return 0, 0, err
	}
	if size > budget {
		return 0, 0, errors.New("upload is larger than 1 GB")
	}
	zr, err := zip.OpenReader(tf.Name())
	if err != nil {
		return 0, 0, errors.New("that is not a zip file")
	}
	defer zr.Close()
	var total int64
	files := 0
	for _, f := range zr.File {
		rel := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if rel == "." || rel == "/" || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(path.Base(rel), "__MACOSX") || strings.Contains(rel, "/__MACOSX/") {
			continue // entries that would leave the upload folder are dropped
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(dst, 0o755)
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return total, files, err
		}
		rc, err := f.Open()
		if err != nil {
			return total, files, err
		}
		mode := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return total, files, err
		}
		n, err := io.Copy(out, io.LimitReader(rc, budget-total+1))
		out.Close()
		rc.Close()
		total += n
		if err != nil {
			return total, files, err
		}
		if total > budget {
			return total, files, fmt.Errorf("upload is larger than 1 GB")
		}
		files++
	}
	return total, files, nil
}

// flatten moves the contents of a lone top-level directory up one level.
func flatten(dir string) {
	for range 3 {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 || !entries[0].IsDir() {
			return
		}
		inner := filepath.Join(dir, entries[0].Name())
		items, err := os.ReadDir(inner)
		if err != nil {
			return
		}
		for _, it := range items {
			if err := os.Rename(filepath.Join(inner, it.Name()), filepath.Join(dir, it.Name())); err != nil {
				return
			}
		}
		_ = os.Remove(inner)
	}
}
