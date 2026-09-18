package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/files"
	"github.com/isletdev/islet/pkg/api"
)

func fileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such file or directory"})
	case errors.Is(err, os.ErrPermission):
		writeJSON(w, http.StatusForbidden, api.Error{Error: "permission", Message: "permission denied"})
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
	}
}

// filesAllowed enforces the role rules: admins do anything; others may read
// outside protected paths; nobody but admins writes.
func (s *Server) filesAllowed(w http.ResponseWriter, r *http.Request, p string, write bool) bool {
	u := userFrom(r.Context())
	if u.Role == "admin" {
		return true
	}
	// Judge the path the reader will actually open, not the text that arrived.
	if c, err := files.Clean(p); err == nil {
		p = c
	}
	if write {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can change files"})
		return false
	}
	if files.IsProtected(p) {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "this path is only visible to admins"})
		return false
	}
	return true
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = files.DefaultRoot()
	}
	if !s.filesAllowed(w, r, p, false) {
		return
	}
	list, err := s.files.List(p)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "entries": list, "protected": files.IsProtected(p)})
}

func (s *Server) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, p, false) {
		return
	}
	b, err := s.files.Read(p)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "content": string(b), "size": len(b)})
}

func (s *Server) handleFilesTail(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, p, false) {
		return
	}
	n := int64(64 << 10)
	if v, err := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64); err == nil && v > 0 && v <= 4<<20 {
		n = v
	}
	b, size, err := s.files.Tail(p, n)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "content": string(b), "size": size})
}

func (s *Server) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeLarge(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	if !s.filesAllowed(w, r, req.Path, true) {
		return
	}
	if err := s.files.Write(req.Path, []byte(req.Content)); err != nil {
		fileErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "file.write", req.Path, fmt.Sprintf("%d bytes", len(req.Content)))
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesOp covers mkdir, touch, rename, copy, delete, trash, chmod, chown,
// archive, extract: one endpoint with an "op" field keeps the API small.
func (s *Server) handleFilesOp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Op        string   `json:"op"`
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		To        string   `json:"to"`
		Mode      string   `json:"mode"`
		Owner     string   `json:"owner"`
		Group     string   `json:"group"`
		Recursive bool     `json:"recursive"`
		Permanent bool     `json:"permanent"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	target := req.Path
	if target == "" && len(req.Paths) > 0 {
		target = req.Paths[0]
	}
	if !s.filesAllowed(w, r, target, true) {
		return
	}
	u := userFrom(r.Context())
	var err error
	var result any
	switch req.Op {
	case "mkdir":
		err = s.files.Mkdir(req.Path)
	case "touch":
		err = s.files.Touch(req.Path)
	case "rename", "move":
		err = s.files.Rename(req.Path, req.To)
	case "copy":
		err = s.files.Copy(req.Path, req.To)
	case "delete":
		paths := req.Paths
		if req.Path != "" {
			paths = append(paths, req.Path)
		}
		var items []files.TrashItem
		for _, p := range paths {
			if req.Permanent {
				err = s.files.Delete(p)
			} else {
				var it *files.TrashItem
				it, err = s.files.Trash(p, u.Username)
				if it != nil {
					items = append(items, *it)
				}
			}
			if err != nil {
				break
			}
		}
		result = items
	case "chmod":
		err = s.files.Chmod(req.Path, req.Mode, req.Recursive)
	case "chown":
		err = files.Chown(req.Path, req.Owner, req.Group)
	case "archive":
		err = s.files.Archive(req.Paths, req.To)
	case "extract":
		err = s.files.Extract(req.Path, req.To)
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "unknown op"})
		return
	}
	if err != nil {
		fileErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "file."+req.Op, target, strings.TrimSpace(req.To+" "+req.Mode))
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "admins only"})
		return
	}
	list, err := s.files.TrashList()
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTrashOp(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "admins only"})
		return
	}
	var req struct {
		Op string `json:"op"`
		ID string `json:"id"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	switch req.Op {
	case "restore":
		it, err := s.files.Restore(req.ID)
		if err != nil {
			fileErr(w, err)
			return
		}
		_ = s.store.Audit(r.Context(), u.Username, "trash.restore", it.Original, "")
		writeJSON(w, http.StatusOK, it)
	case "purge":
		if err := s.files.PurgeTrash(req.ID); err != nil {
			fileErr(w, err)
			return
		}
		_ = s.store.Audit(r.Context(), u.Username, "trash.purge", req.ID, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "unknown op"})
	}
}

// handleFilesDownload streams a file, or a zip of a directory.
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, p, false) {
		return
	}
	e, err := s.files.Stat(p)
	if err != nil {
		fileErr(w, err)
		return
	}
	name := filepath.Base(e.Path)
	if e.IsDir {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".zip"))
		_ = files.Zip(w, []string{e.Path})
		return
	}
	f, err := os.Open(e.Path)
	if err != nil {
		fileErr(w, err)
		return
	}
	defer f.Close()
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" && (strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "text/") || ct == "application/pdf") {
		disp = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disp, name))
	http.ServeContent(w, r, name, time.Time{}, f)
}

// handleFilesUpload accepts multipart files into ?path=<dir>.
func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, dir, true) {
		return
	}
	dir, err := files.Clean(dir)
	if err != nil {
		fileErr(w, err)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "multipart body expected"})
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "1"
	var saved []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fileErr(w, err)
			return
		}
		name := filepath.Base(part.FileName())
		if name == "" || name == "." || name == ".." {
			continue
		}
		dst := filepath.Join(dir, name)
		flags := os.O_CREATE | os.O_WRONLY | os.O_EXCL
		if overwrite {
			flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		}
		f, err := os.OpenFile(dst, flags, 0o644)
		if err != nil {
			fileErr(w, err)
			return
		}
		_, err = io.Copy(f, part)
		f.Close()
		if err != nil {
			fileErr(w, err)
			return
		}
		saved = append(saved, dst)
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "file.upload", dir, fmt.Sprintf("%d files", len(saved)))
	writeJSON(w, http.StatusOK, map[string]any{"saved": saved})
}

func (s *Server) handleFilesSearch(w http.ResponseWriter, r *http.Request) {
	// path and query, not root and q: every other route under /api/v1/files
	// takes `path`, and a caller that has to remember which one this is will
	// get it wrong — the search tool did, and was told "path must be absolute"
	// about a path that was, because it arrived under a name nothing read.
	root := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, root, false) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hits, err := s.files.Search(ctx, root, r.URL.Query().Get("query"), r.URL.Query().Get("content") == "1", 500)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

func (s *Server) handleFilesUsage(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, p, false) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	u, err := s.files.DiskUsage(ctx, p)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleFilesChecksum(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.filesAllowed(w, r, p, false) {
		return
	}
	sum, err := s.files.Checksum(p)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": p, "sha256": sum})
}
