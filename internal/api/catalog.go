package api

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

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

// handleInstalledUpdates compares each installed app's images with the
// registry and reports which have a newer digest.
func (s *Server) handleInstalledUpdates(w http.ResponseWriter, r *http.Request) {
	list, err := s.catalog.InstalledApps()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	actor := userFrom(r.Context()).Username
	out := map[string][]string{}
	for _, it := range list {
		compose, _, err := s.docker.ReadStack(it.Name)
		if err != nil {
			continue
		}
		for _, img := range composeImages(compose) {
			local, err := s.runner.Run(r.Context(), actor, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", img)
			if err != nil {
				continue
			}
			remote, err := s.runner.Run(r.Context(), actor, "docker", "buildx", "imagetools", "inspect", "--format", "{{.Manifest.Digest}}", img)
			if err != nil {
				continue
			}
			ld := strings.TrimSpace(local.Stdout)
			rd := strings.TrimSpace(remote.Stdout)
			if i := strings.Index(ld, "@"); i >= 0 {
				ld = ld[i+1:]
			}
			if rd != "" && ld != "" && rd != ld {
				out[it.Name] = append(out[it.Name], img)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
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
	rc, wait, err := s.docker.StackAction(r.Context(), u.Username, name, "pull")
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, _ = io.Copy(pw, rc)
		if err := wait(); err != nil {
			fmt.Fprintln(pw, "pull failed: "+err.Error())
			return
		}
		rc2, wait2, err := s.docker.StackAction(r.Context(), u.Username, name, "up")
		if err != nil {
			fmt.Fprintln(pw, err.Error())
			return
		}
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
