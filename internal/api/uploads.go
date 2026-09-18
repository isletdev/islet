package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/isletdev/islet/internal/uploads"
	"github.com/isletdev/islet/pkg/api"
)

// The files somebody hands the assistant.
//
// They are ordinary files on this server from the moment they arrive, which is
// what makes the feature small: the assistant is given a path, and every tool
// it already has takes paths. Nothing here encodes a file for a model, and
// nothing keeps a second copy of it in a database.

// handleUploads lists what has been uploaded, and says what checked it.
func (s *Server) handleUploads(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		writeJSON(w, http.StatusOK, api.Uploads{Files: []api.Upload{}, MaxSize: uploads.MaxSize})
		return
	}
	if r.Method == http.MethodPost {
		s.handleUploadCreate(w, r)
		return
	}
	list, err := s.uploads.List()
	if err != nil {
		s.failed(w, "uploads", err)
		return
	}
	writeJSON(w, http.StatusOK, api.Uploads{
		Files:   uploadsOut(list),
		Scanner: s.uploads.Scanner(r.Context()),
		MaxSize: uploads.MaxSize,
	})
}

// handleUploadCreate takes one or more files out of a multipart body.
//
// Streamed part by part rather than through ParseMultipartForm: a video is a
// perfectly ordinary thing to hand a site builder, and the form parser would
// hold it in memory or in a temporary file first, on a server that may have a
// gigabyte of RAM in total.
func (s *Server) handleUploadCreate(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "multipart body expected"})
		return
	}
	actor := userFrom(r.Context()).Username
	saved := []api.Upload{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		f, err := s.uploads.Save(r.Context(), actor, part.FileName(), part)
		part.Close()
		if err != nil {
			// Whatever arrived before this one is already saved and already
			// has a path; the error says which file was refused so the panel
			// can mark that one rather than the batch.
			uploadErr(w, part.FileName(), err)
			return
		}
		_ = s.store.Audit(r.Context(), actor, "assistant.upload", f.Name,
			fmt.Sprintf("%d bytes, %s, scanned by %s", f.Size, f.Type, orNone(f.Scanner)))
		saved = append(saved, uploadOut(*f))
	}
	if len(saved) == 0 {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "no file was sent"})
		return
	}
	writeJSON(w, http.StatusCreated, api.Uploads{Files: saved, Scanner: s.uploads.Scanner(r.Context()), MaxSize: uploads.MaxSize})
}

// handleUpload1 serves one upload's bytes, or deletes it.
//
// The bytes are here so the composer can show a thumbnail of what is about to
// be sent. A name next to a paperclip is not enough to notice that the wrong
// photograph is attached, and noticing afterwards means it is already on the
// server and in a conversation.
func (s *Server) handleUpload1(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such upload"})
		return
	}
	id := r.PathValue("id")
	f, err := s.uploads.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such upload"})
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.uploads.Remove(id); err != nil {
			s.failed(w, "uploads", err)
			return
		}
		_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "assistant.upload.delete", f.Name, "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	file, err := os.Open(f.Path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such upload"})
		return
	}
	defer file.Close()
	// Attachment, always, whatever it is. The panel fetches these into an
	// object URL for the thumbnail; a browser navigating to one directly must
	// not render somebody's uploaded HTML as a page on the panel's own origin.
	w.Header().Set("Content-Type", f.Type)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.Name))
	http.ServeContent(w, r, f.Name, time.Time{}, file)
}

// uploadErr turns a refusal into the status that says why.
func uploadErr(w http.ResponseWriter, name string, err error) {
	var inf *uploads.Infected
	switch {
	case errors.As(err, &inf):
		// 422: the request was fine, the file is not.
		writeJSON(w, http.StatusUnprocessableEntity, api.Error{Error: "infected", Message: nameIt(name, inf.Error())})
	case errors.Is(err, uploads.ErrTooBig):
		writeJSON(w, http.StatusRequestEntityTooLarge, api.Error{Error: "too_big", Message: nameIt(name, err.Error())})
	case errors.Is(err, uploads.ErrNoName):
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
	default:
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "failed", Message: nameIt(name, err.Error())})
	}
}

func nameIt(name, msg string) string {
	if name = uploads.SafeName(name); name == "" {
		return msg
	}
	return name + ": " + msg
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

func uploadOut(f uploads.File) api.Upload {
	return api.Upload{ID: f.ID, Name: f.Name, Path: f.Path, Size: f.Size, Type: f.Type, AddedAt: f.AddedAt, Scanner: f.Scanner}
}

func uploadsOut(list []uploads.File) []api.Upload {
	out := make([]api.Upload, 0, len(list))
	for _, f := range list {
		out = append(out, uploadOut(f))
	}
	return out
}
