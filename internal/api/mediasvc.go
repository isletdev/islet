package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/media"
	"github.com/isletdev/islet/pkg/api"
)

// The service API: the one part of this daemon that answers to an application
// rather than to the person who owns the server.
//
// It is a separate router with a separate middleware chain, and that is the
// point rather than an implementation detail. Nothing here looks at a session
// cookie, nothing here can reach a route under /api/v1, and a media key is not
// a thing `requireAuth` has ever heard of. Two authorities that must never meet
// do not share a mux.
//
// It is reachable two ways. Under /svc/ on whatever address the daemon answers
// on, which is what an application on this same server uses over localhost or
// the Docker network; and at the root of a hostname the operator points at it,
// which is what a browser uses — because the panel's origin holds a session
// cookie and nothing serving user-uploaded bytes belongs on it.

const svcPrefix = "/svc/"

// serviceHandler is the whole app-facing surface.
func (s *Server) serviceHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /svc/media/v1/health", s.svcMediaHealth)
	mux.HandleFunc("POST /svc/media/v1/upload", s.svcMediaUpload)
	mux.HandleFunc("POST /svc/media/v1/tickets", s.svcMediaTicket)
	mux.HandleFunc("POST /svc/media/v1/tickets/complete", s.svcMediaComplete)
	mux.HandleFunc("GET /svc/media/v1/objects/{id}", s.svcMediaGet)
	mux.HandleFunc("GET /svc/media/v1/objects/{id}/{preset}", s.svcMediaGet)
	mux.HandleFunc("POST /svc/media/v1/objects/{id}/sign", s.svcMediaSign)
	mux.HandleFunc("DELETE /svc/media/v1/objects/{id}", s.svcMediaDelete)
	mux.HandleFunc("/svc/", s.svcNotFound)
	return s.recover(s.logRequests(s.svcCORS(mux)))
}

// svcRoot rewrites a request that arrived on the service's own hostname.
//
// The operator points media.example.com at the daemon and their application
// calls https://media.example.com/v1/upload. Inside, that is the same route as
// /svc/media/v1/upload — one set of handlers, reachable by the name that suits
// the caller.
func (s *Server) svcHost(r *http.Request) bool {
	if s.media == nil {
		return false
	}
	host := strings.ToLower(r.Host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	want := strings.ToLower(strings.TrimSpace(s.media.Settings(r.Context()).Host))
	return want != "" && host == want
}

// svcCORS answers the preflight and sets the headers on the way out.
//
// Per key, not per server: one application's origins are not another's, and a
// key with no origins listed is a server-side key that no browser should be able
// to use at all. The permissive default would be the one that turns a leaked key
// in a web page into somebody else's upload quota.
func (s *Server) svcCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if k := s.svcKeyFor(r); k != nil && k.AllowsOrigin(origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Islet-Filename, X-Islet-Visibility")
				h.Set("Access-Control-Max-Age", "600")
				h.Add("Vary", "Origin")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// svcKeyFor reads the key off a request without deciding anything about it.
func (s *Server) svcKeyFor(r *http.Request) *media.Key {
	if s.media == nil {
		return nil
	}
	secret := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
	if secret == "" {
		secret = r.URL.Query().Get("key")
	}
	if secret == "" {
		return nil
	}
	k, err := s.media.Authenticate(r.Context(), secret)
	if err != nil {
		return nil
	}
	return k
}

// svcKey is svcKeyFor with the refusals: enabled, authenticated, allowed from
// this origin, inside its rate limit, and holding the scope being used.
func (s *Server) svcKey(w http.ResponseWriter, r *http.Request, scope string) (*media.Key, bool) {
	if s.media == nil || !s.media.Enabled(r.Context()) {
		svcErr(w, http.StatusNotFound, "disabled", "the media service is not enabled on this server")
		return nil, false
	}
	k := s.svcKeyFor(r)
	if k == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="islet-media"`)
		svcErr(w, http.StatusUnauthorized, "unauthorized", "send a media key as a bearer token")
		return nil, false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !k.AllowsOrigin(origin) {
		svcErr(w, http.StatusForbidden, "forbidden", "this key is not allowed from "+origin)
		return nil, false
	}
	if !s.media.Allow(k) {
		w.Header().Set("Retry-After", "10")
		svcErr(w, http.StatusTooManyRequests, "rate_limited", "this key is going too fast")
		return nil, false
	}
	if !k.Can(scope) {
		svcErr(w, http.StatusForbidden, "forbidden", "this key may not "+scope)
		return nil, false
	}
	return k, true
}

func (s *Server) svcMediaHealth(w http.ResponseWriter, r *http.Request) {
	if s.media == nil || !s.media.Enabled(r.Context()) {
		svcErr(w, http.StatusNotFound, "disabled", "the media service is not enabled on this server")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "media", "version": 1})
}

// svcMediaUpload takes a file the way both kinds of caller send one: a browser
// sends multipart, a backend usually sends the bytes with a filename header,
// and either is less work than making them agree.
func (s *Server) svcMediaUpload(w http.ResponseWriter, r *http.Request) {
	k, ok := s.svcKey(w, r, "upload")
	if !ok {
		return
	}
	b, err := s.svcBucket(r, k)
	if err != nil {
		svcErr(w, http.StatusBadRequest, "no_bucket", err.Error())
		return
	}
	visibility := r.URL.Query().Get("visibility")
	if visibility == "" {
		visibility = r.Header.Get("X-Islet-Visibility")
	}

	var (
		body     io.Reader = r.Body
		filename           = strings.TrimSpace(r.Header.Get("X-Islet-Filename"))
		ctype              = r.Header.Get("Content-Type")
	)
	if strings.HasPrefix(ctype, "multipart/") {
		mr, err := r.MultipartReader()
		if err != nil {
			svcErr(w, http.StatusBadRequest, "invalid", "multipart body expected")
			return
		}
		part, err := nextFilePart(mr)
		if err != nil {
			svcErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		defer part.Close()
		body, filename, ctype = part, part.FileName(), part.Header.Get("Content-Type")
	}
	if filename == "" {
		filename = r.URL.Query().Get("filename")
	}

	obj, err := s.media.Upload(r.Context(), "key:"+k.Name, k, b, filename, ctype, body, visibility)
	if err != nil {
		svcUploadErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.svcObject(r, obj))
}

// svcMediaTicket hands back a URL the caller uploads to directly.
func (s *Server) svcMediaTicket(w http.ResponseWriter, r *http.Request) {
	k, ok := s.svcKey(w, r, "upload")
	if !ok {
		return
	}
	var req struct{ Filename, ContentType, Visibility string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	b, err := s.svcBucket(r, k)
	if err != nil {
		svcErr(w, http.StatusBadRequest, "no_bucket", err.Error())
		return
	}
	t, err := s.media.Ticket(r.Context(), k, b, req.Filename, req.ContentType, req.Visibility)
	if err != nil {
		svcErr(w, http.StatusBadRequest, "no_ticket", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// svcMediaComplete records an upload that went straight to the bucket.
func (s *Server) svcMediaComplete(w http.ResponseWriter, r *http.Request) {
	k, ok := s.svcKey(w, r, "upload")
	if !ok {
		return
	}
	var req struct{ Handle string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	b, err := s.svcBucket(r, k)
	if err != nil {
		svcErr(w, http.StatusBadRequest, "no_bucket", err.Error())
		return
	}
	obj, err := s.media.Adopt(r.Context(), "key:"+k.Name, k, b, req.Handle)
	if err != nil {
		svcUploadErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.svcObject(r, obj))
}

// svcMediaGet serves an object, at a preset when one is named.
//
// A public object needs no key: that is what public means, and requiring one
// would mean every <img> tag on somebody's site carrying a credential. A private
// one needs a signed link or a key that may read.
func (s *Server) svcMediaGet(w http.ResponseWriter, r *http.Request) {
	if s.media == nil || !s.media.Enabled(r.Context()) {
		svcErr(w, http.StatusNotFound, "disabled", "the media service is not enabled on this server")
		return
	}
	// A public object needs no key, so the limit here is by address. Without
	// it the one unauthenticated route is also the one that can be made to
	// convert an image, and a loop is cheaper to write than to serve.
	if k := s.svcKeyFor(r); k != nil {
		if !s.media.Allow(k) {
			w.Header().Set("Retry-After", "10")
			svcErr(w, http.StatusTooManyRequests, "rate_limited", "this key is going too fast")
			return
		}
	} else if !s.media.AllowAnon(clientIP(r)) {
		w.Header().Set("Retry-After", "10")
		svcErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests from this address")
		return
	}
	id, preset := r.PathValue("id"), r.PathValue("preset")
	obj, err := s.media.Object(r.Context(), id)
	if err != nil {
		svcErr(w, http.StatusNotFound, "not_found", "no such object")
		return
	}
	if obj.Visibility != "public" {
		q := r.URL.Query()
		signed := s.media.Verify(r.Context(), id, preset, q.Get("exp"), q.Get("sig"))
		if !signed {
			k := s.svcKeyFor(r)
			if k == nil || !k.Can("read") || !sameNamespace(k, obj) {
				svcErr(w, http.StatusForbidden, "forbidden", "this object is private")
				return
			}
		}
	}
	ren, err := s.media.Open(r.Context(), "svc", obj, preset)
	if err != nil {
		svcObjectErr(w, err)
		return
	}
	// Never changes: the id is in the URL and a new upload is a new id. A year
	// is what makes a CDN in front of this worth having.
	if ren.Immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=60")
	}
	if ren.Redirect != "" {
		http.Redirect(w, r, ren.Redirect, http.StatusFound)
		return
	}
	defer ren.Body.Close()
	if ren.ContentType != "" {
		w.Header().Set("Content-Type", ren.ContentType)
	}
	if ren.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(ren.Size, 10))
	}
	// Uploaded bytes, served from a host applications trust: nothing here is
	// ever to be sniffed into a script or rendered as a page.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if !strings.HasPrefix(ren.ContentType, "image/") && !strings.HasPrefix(ren.ContentType, "video/") && !strings.HasPrefix(ren.ContentType, "audio/") && ren.ContentType != "application/pdf" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", obj.Filename))
	}
	_, _ = io.Copy(w, ren.Body)
}

// svcMediaSign mints a link to a private object that works for a while.
func (s *Server) svcMediaSign(w http.ResponseWriter, r *http.Request) {
	k, ok := s.svcKey(w, r, "sign")
	if !ok {
		return
	}
	obj, err := s.media.Object(r.Context(), r.PathValue("id"))
	if err != nil || !sameNamespace(k, obj) {
		svcErr(w, http.StatusNotFound, "not_found", "no such object")
		return
	}
	var req struct {
		Preset  string `json:"preset"`
		Seconds int    `json:"seconds"`
	}
	_ = decode(r, &req)
	ttl := time.Duration(req.Seconds) * time.Second
	q, err := s.media.Sign(r.Context(), obj.ID, req.Preset, ttl)
	if err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.svcObjectURL(r, obj.ID, req.Preset) + "?" + q})
}

func (s *Server) svcMediaDelete(w http.ResponseWriter, r *http.Request) {
	k, ok := s.svcKey(w, r, "delete")
	if !ok {
		return
	}
	obj, err := s.media.Object(r.Context(), r.PathValue("id"))
	if err != nil || !sameNamespace(k, obj) {
		svcErr(w, http.StatusNotFound, "not_found", "no such object")
		return
	}
	if err := s.media.Delete(r.Context(), "key:"+k.Name, obj); err != nil {
		s.failed(w, "media", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) svcNotFound(w http.ResponseWriter, r *http.Request) {
	svcErr(w, http.StatusNotFound, "not_found", "no such service route")
}

// ---- helpers --------------------------------------------------------------

// svcBucket is where this key's uploads go: the one it is tied to, or the
// server's default.
func (s *Server) svcBucket(r *http.Request, k *media.Key) (*media.Bucket, error) {
	if k.BucketID != "" {
		return s.media.Bucket(r.Context(), k.BucketID)
	}
	b, err := s.media.DefaultBucket(r.Context())
	if errors.Is(err, media.ErrNotFound) {
		return nil, errors.New("this server has no media bucket configured yet")
	}
	return b, err
}

// sameNamespace is the wall between two applications on one server.
func sameNamespace(k *media.Key, o *media.Object) bool {
	return k.Namespace == "" || k.Namespace == o.Namespace
}

func (s *Server) svcObjectURL(r *http.Request, id, preset string) string {
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	base := scheme + "://" + r.Host
	if s.svcHost(r) {
		base += "/v1/objects/" + id
	} else {
		base += "/svc/media/v1/objects/" + id
	}
	if preset != "" {
		base += "/" + preset
	}
	return base
}

// svcObject is what an application gets back: what it uploaded, and the URLs it
// can use without building them itself.
func (s *Server) svcObject(r *http.Request, o *media.Object) map[string]any {
	out := map[string]any{
		"id": o.ID, "filename": o.Filename, "contentType": o.ContentType,
		"size": o.Size, "checksum": o.Checksum, "visibility": o.Visibility,
		"createdAt": o.CreatedAt, "url": s.svcObjectURL(r, o.ID, ""),
	}
	if o.Width > 0 {
		out["width"], out["height"] = o.Width, o.Height
	}
	if o.Scanner != "" {
		out["scannedBy"] = o.Scanner
	} else {
		// Said rather than left out: "no virus was found" and "nothing looked"
		// are different answers and only one of them is reassuring.
		out["scannedBy"] = nil
	}
	// Anything the converters can take a picture of gets the sizes offered:
	// an image, a video's frame, a PDF's first page. Offering them only for
	// images would hide a thumbnail that already works.
	if presets, err := s.media.Presets(r.Context()); err == nil && thumbnailable(o.ContentType) {
		variants := map[string]string{}
		for _, p := range presets {
			variants[p.Name] = s.svcObjectURL(r, o.ID, p.Name)
		}
		out["variants"] = variants
	}
	return out
}

// thumbnailable is what derive() knows how to produce a picture from.
func thumbnailable(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		contentType == "application/pdf"
}

func svcErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, api.Error{Error: kind, Message: msg})
}

func svcUploadErr(w http.ResponseWriter, err error) {
	var inf *media.Infected
	switch {
	case errors.As(err, &inf):
		svcErr(w, http.StatusUnprocessableEntity, "infected", inf.Error())
	case errors.Is(err, media.ErrTooBig):
		svcErr(w, http.StatusRequestEntityTooLarge, "too_big", err.Error())
	case errors.Is(err, media.ErrType):
		svcErr(w, http.StatusUnsupportedMediaType, "type_not_allowed", err.Error())
	case errors.Is(err, media.ErrQuota):
		svcErr(w, http.StatusInsufficientStorage, "quota", err.Error())
	case errors.Is(err, media.ErrForbidden):
		svcErr(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		svcErr(w, http.StatusBadGateway, "failed", err.Error())
	}
}

func svcObjectErr(w http.ResponseWriter, err error) {
	if errors.Is(err, media.ErrNotFound) || errors.Is(err, media.ErrNoObject) {
		svcErr(w, http.StatusNotFound, "not_found", "no such object")
		return
	}
	svcErr(w, http.StatusBadGateway, "failed", err.Error())
}
