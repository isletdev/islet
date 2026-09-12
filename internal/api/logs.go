package api

import (
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// LogSource is something the Logs page can tail.
type LogSource struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
}

// handleLogSources lists journals, files and containers that can be tailed.
func (s *Server) handleLogSources(w http.ResponseWriter, r *http.Request) {
	out := []LogSource{}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("journalctl"); err == nil {
			out = append(out,
				LogSource{ID: "journal", Label: "System journal", Group: "System"},
				LogSource{ID: "unit:isletd", Label: "Islet daemon", Group: "System"},
				LogSource{ID: "unit:docker", Label: "Docker daemon", Group: "System"},
				LogSource{ID: "unit:ssh", Label: "SSH logins", Group: "System"},
				LogSource{ID: "kernel", Label: "Kernel", Group: "System"},
			)
		}
		for _, f := range []struct{ path, label string }{{"/var/log/auth.log", "Auth log"}, {"/var/log/syslog", "Syslog"}, {"/var/log/nginx/error.log", "nginx errors"}, {"/var/log/nginx/access.log", "nginx access"}} {
			if _, err := os.Stat(f.path); err == nil {
				out = append(out, LogSource{ID: "file:" + f.path, Label: f.label, Group: "Files"})
			}
		}
	}
	u := userFrom(r.Context())
	if scoped(u) {
		out = out[:0] // journals and files are host-wide
	}
	if list, err := s.docker.Containers(r.Context(), u.Username); err == nil {
		for _, c := range list {
			if scoped(u) && !s.allowsContainer(r.Context(), u, c.Name) {
				continue
			}
			label := c.Name
			if c.Name == proxy.ContainerName {
				label = "Islet proxy (Traefik)"
			}
			out = append(out, LogSource{ID: "container:" + c.Name, Label: label, Group: "Containers"})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLogStream tails one source as SSE. ?source=…&tail=200&follow=1&grep=text
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src := q.Get("source")
	tail := 200
	if v, err := strconv.Atoi(q.Get("tail")); err == nil && v >= 0 && v <= 5000 {
		tail = v
	}
	follow := q.Get("follow") == "1"
	actor := userFrom(r.Context()).Username
	kind, arg, _ := cut(src, ":")
	var argv []string
	if u := userFrom(r.Context()); scoped(u) && (kind != "container" || !s.allowsContainer(r.Context(), u, arg)) {
		forbiddenScope(w)
		return
	}
	switch kind {
	case "container":
		if _, err := s.docker.Inspect(r.Context(), actor, arg); err != nil {
			s.dockerErr(w, err)
			return
		}
		rc, wait, err := s.docker.Logs(r.Context(), actor, arg, tail, follow)
		if err != nil {
			s.dockerErr(w, err)
			return
		}
		streamLines(w, r, rc, wait)
		return
	case "journal":
		argv = []string{"journalctl", "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(tail)}
	case "unit":
		if !docker.ValidName(arg) {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "bad unit name"})
			return
		}
		argv = []string{"journalctl", "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(tail), "-u", arg}
	case "kernel":
		argv = []string{"journalctl", "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(tail), "-k"}
	case "file":
		allowed := map[string]bool{"/var/log/auth.log": true, "/var/log/syslog": true, "/var/log/nginx/error.log": true, "/var/log/nginx/access.log": true}
		if !allowed[arg] {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "unknown log file"})
			return
		}
		argv = []string{"tail", "-n", strconv.Itoa(tail)}
		if follow {
			argv = append(argv, "-F")
		}
		argv = append(argv, arg)
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "unknown source"})
		return
	}
	if follow && kind != "file" {
		argv = append(argv, "-f")
	}
	if g := q.Get("grep"); g != "" && (kind == "journal" || kind == "unit" || kind == "kernel") {
		argv = append(argv, "-g", g)
	}
	rc, wait, err := s.runner.Stream(r.Context(), actor, argv[0], argv[1:]...)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "logs", Message: err.Error()})
		return
	}
	streamLines(w, r, rc, wait)
}

func cut(s, sep string) (string, string, bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
