package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := s.catalog.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCatalogApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.catalog.Get(r.PathValue("slug"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot install apps"})
		return
	}
	var req catalog.InstallRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	req.Slug = r.PathValue("slug")
	rc, wait, err := s.catalog.Install(r.Context(), u.Username, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "app.install", req.Slug, "name="+req.Name+" domain="+req.Domain)
	streamLines(w, r, rc, wait)
}

// digestOf pulls a sha256 digest out of what a docker command printed, and
// returns "" for anything that is not one.
//
// Being strict is the point. Both commands have a way of answering with
// something other than a digest — a name@digest reference, a JSON string, a
// whole report — and a comparison against text we did not understand reads as
// "there is an update" every single time.
func digestOf(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if !strings.HasPrefix(s, "sha256:") || strings.ContainsAny(s, " \t\r\n") {
		return ""
	}
	return s
}

// handleInstalledUpdates compares each installed app's images with the
// registry and reports which have a newer digest.
func (s *Server) handleInstalledUpdates(w http.ResponseWriter, r *http.Request) {
	list, err := s.catalog.InstalledApps()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	actor := userFrom(r.Context()).Username
	if cached, ok := s.updateCache.get(); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// Every image asks a registry whether there is something newer, and a
	// registry over the internet is the slow part. Serially, two apps on the
	// development server took 4.8 seconds and ten would have taken twenty —
	// with the page waiting on all of it. They are asked together now, with a
	// ceiling on how many at once so a server with forty images does not open
	// forty connections, and with a deadline of its own so a registry that
	// hangs delays the answer rather than holding the request open.
	type job struct{ app, image string }
	var jobs []job
	for _, it := range list {
		compose, _, err := s.docker.ReadStack(it.Name)
		if err != nil {
			continue
		}
		for _, img := range composeImages(compose) {
			jobs = append(jobs, job{it.Name, img})
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), updateCheckTimeout)
	defer cancel()
	var (
		mu  sync.Mutex
		out = map[string][]string{}
		wg  sync.WaitGroup
	)
	gate := make(chan struct{}, 6)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			local, err := s.runner.Run(ctx, actor, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", j.image)
			if err != nil {
				return
			}
			// `{{json .Manifest.Digest}}` rather than `{{.Manifest.Digest}}`:
			// buildx ignores the plain template and prints its whole human
			// report instead, which never equals a digest, so every app
			// claimed an update was waiting for it forever.
			remote, err := s.runner.Run(ctx, actor, "docker", "buildx", "imagetools", "inspect", "--format", "{{json .Manifest.Digest}}", j.image)
			if err != nil {
				return
			}
			ld, rd := digestOf(local.Stdout), digestOf(remote.Stdout)
			if rd == "" || ld == "" || rd == ld {
				return
			}
			mu.Lock()
			out[j.app] = append(out[j.app], j.image)
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	s.updateCache.put(out)
	writeJSON(w, http.StatusOK, out)
}

// How long the whole check may take, and how long its answer is good for.
//
// Images are not published minute by minute, and this runs whenever somebody
// opens the page: without a memory it asked every registry again each time the
// tab was refreshed.
const (
	updateCheckTimeout = 25 * time.Second
	updateCacheFor     = 15 * time.Minute
)

// updateCheck remembers the last answer.
type updateCheck struct {
	mu   sync.Mutex
	at   time.Time
	last map[string][]string
}

func (u *updateCheck) get() (map[string][]string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.last == nil || time.Since(u.at) > updateCacheFor {
		return nil, false
	}
	// A copy: the caller writes it out as JSON while another request may be
	// replacing it.
	out := make(map[string][]string, len(u.last))
	for k, v := range u.last {
		out[k] = append([]string(nil), v...)
	}
	return out, true
}

func (u *updateCheck) put(v map[string][]string) {
	u.mu.Lock()
	u.last, u.at = v, time.Now()
	u.mu.Unlock()
}

var imageLineRe = regexp.MustCompile(`(?m)^\s*image:\s*["']?([^"'\s#]+)`)

func composeImages(compose string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range imageLineRe.FindAllStringSubmatch(compose, -1) {
		if img := m[1]; !seen[img] && !strings.Contains(img, "${") {
			seen[img] = true
			out = append(out, img)
		}
	}
	return out
}

// handleInstalledUpdate pulls newer images and recreates the stack.
func (s *Server) handleInstalledUpdate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot update apps"})
		return
	}
	name := r.PathValue("name")
	// The update outlives the request on purpose. Closing the tab between the
	// pull and the "up" used to cancel the second half, which left the stack
	// down with its images already replaced.
	bg := context.WithoutCancel(r.Context())
	rc, wait, err := s.docker.StackAction(bg, u.Username, name, "pull")
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer rc.Close()
		_, _ = io.Copy(pw, rc)
		if err := wait(); err != nil {
			fmt.Fprintln(pw, "pull failed: "+err.Error())
			return
		}
		rc2, wait2, err := s.docker.StackAction(bg, u.Username, name, "up")
		if err != nil {
			fmt.Fprintln(pw, err.Error())
			return
		}
		defer rc2.Close()
		_, _ = io.Copy(pw, rc2)
		_ = wait2()
	}()
	_ = s.store.Audit(r.Context(), u.Username, "app.update", name, "")
	streamLines(w, r, pr, func() error { return nil })
}

func (s *Server) handleInstalledApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.catalog.InstalledApps()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if userFrom(r.Context()).Role != "admin" {
		for i := range list {
			list[i].Values = nil
		}
	}
	writeJSON(w, http.StatusOK, list)
}
