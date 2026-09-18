package api

import (
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/media"
	"github.com/isletdev/islet/pkg/api"
)

// The operator's half of the media service: switching it on, saying where bytes
// go, minting the keys applications hold, and looking at what is there.
//
// Everything here is an admin route under /api/v1 behind a panel session. The
// half applications talk to is in mediasvc.go and shares none of it.

func (s *Server) mediaOK(w http.ResponseWriter) bool {
	if s.media == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Error{Error: "unavailable", Message: "the media service is not available on this daemon"})
		return false
	}
	return true
}

// handleMedia is the service's own page: is it on, where does it answer, and
// are the converters installed.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) {
		return
	}
	if r.Method == http.MethodPost {
		if !s.adminOnly(w, r) {
			return
		}
		var req media.Settings
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		if err := s.media.SaveSettings(r.Context(), req); err != nil {
			s.failed(w, "media", err)
			return
		}
		_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "media.settings", "media", boolWord(req.Enabled))
	}
	buckets, _ := s.media.Buckets(r.Context())
	usage, _ := s.media.Usage(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": s.media.Settings(r.Context()),
		"tools":    s.media.WorkerStatus(r.Context()),
		"buckets":  len(buckets),
		"usage":    usage,
		"maxBytes": media.DefaultMaxBytes,
	})
}

// handleMediaWorker installs or removes the converters. Streamed, because
// building the image on a small server takes minutes.
func (s *Server) handleMediaWorker(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) || !s.adminOnly(w, r) {
		return
	}
	actor := userFrom(r.Context()).Username
	if r.Method == http.MethodDelete {
		if err := s.media.RemoveWorker(r.Context(), actor); err != nil {
			s.failed(w, "media", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		s.failed(w, "media", errors.New("this connection cannot stream"))
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	// no-transform because a compressor in front of this would buffer it, and
	// the whole point of streaming a build is that it arrives while it happens.
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.WriteHeader(http.StatusOK)
	line := func(text string) {
		writeLine(w, map[string]string{"line": text})
		fl.Flush()
	}
	if err := s.media.InstallWorker(r.Context(), actor, line); err != nil {
		writeLine(w, map[string]any{"error": err.Error()})
		fl.Flush()
		return
	}
	writeLine(w, map[string]any{"ok": true})
	fl.Flush()
}

func (s *Server) handleMediaBuckets(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) {
		return
	}
	if r.Method == http.MethodPost {
		if !s.adminOnly(w, r) {
			return
		}
		var req struct {
			media.Bucket
			Secret string `json:"secret"`
		}
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		b, err := s.media.SaveBucket(r.Context(), userFrom(r.Context()).Username, &req.Bucket, req.Secret)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, b)
		return
	}
	list, err := s.media.Buckets(r.Context())
	if err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleMediaBucket1(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) || !s.adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodDelete {
		if err := s.media.RemoveBucket(r.Context(), userFrom(r.Context()).Username, id); err != nil {
			writeJSON(w, http.StatusConflict, api.Error{Error: "in_use", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	// A check writes a small object, reads it back and removes it, so a wrong
	// credential is found on the screen where it was typed rather than by an
	// application at three in the morning.
	if err := s.media.CheckBucket(r.Context(), id); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "unreachable", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMediaKeys(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) {
		return
	}
	if r.Method == http.MethodPost {
		if !s.adminOnly(w, r) {
			return
		}
		var req media.Key
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		k, err := s.media.Mint(r.Context(), userFrom(r.Context()).Username, &req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		// The secret is in this response and nowhere else, ever again.
		writeJSON(w, http.StatusCreated, k)
		return
	}
	list, err := s.media.Keys(r.Context())
	if err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleMediaKey1(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) || !s.adminOnly(w, r) {
		return
	}
	if err := s.media.RemoveKey(r.Context(), userFrom(r.Context()).Username, r.PathValue("id")); err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMediaPresets(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) {
		return
	}
	if r.Method == http.MethodPost {
		if !s.adminOnly(w, r) {
			return
		}
		var req media.Preset
		if err := decode(r, &req); err != nil {
			s.badJSON(w, err)
			return
		}
		p, err := s.media.SavePreset(r.Context(), userFrom(r.Context()).Username, &req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	}
	list, err := s.media.Presets(r.Context())
	if err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleMediaPreset1(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) || !s.adminOnly(w, r) {
		return
	}
	if err := s.media.RemovePreset(r.Context(), userFrom(r.Context()).Username, r.PathValue("id")); err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMediaObjects(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) {
		return
	}
	list, err := s.media.Objects(r.Context(), r.URL.Query().Get("namespace"), atoiDefault(r.URL.Query().Get("limit"), 100))
	if err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleMediaObject1(w http.ResponseWriter, r *http.Request) {
	if !s.mediaOK(w) || !s.adminOnly(w, r) {
		return
	}
	obj, err := s.media.Object(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such object"})
		return
	}
	if err := s.media.Delete(r.Context(), userFrom(r.Context()).Username, obj); err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- small shared things --------------------------------------------------

func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// nextFilePart finds the first part of a multipart body that is a file, so a
// form that also carries text fields uploads what was meant.
func nextFilePart(mr *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, errors.New("no file was sent")
		}
		if strings.TrimSpace(part.FileName()) != "" {
			return part, nil
		}
		part.Close()
	}
}

// writeLine is one NDJSON line. The install stream is a few lines over several
// minutes, so nothing is buffered on the way out.
func writeLine(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return d
}
