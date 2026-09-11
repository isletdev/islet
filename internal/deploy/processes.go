package deploy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Process is an extra long-running command started from the same image as
// the web process (a worker, a scheduler), with an instance count.
type Process struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Cmd   string `json:"cmd"`
}

var processRe = regexp.MustCompile(`^([a-z][a-z0-9-]{0,19})(?:\s*x\s*(\d{1,2}))?\s*:\s*(.+)$`)

// ParseProcesses reads one process per line: "worker: node worker.js" or
// "worker x2: node worker.js".
func ParseProcesses(s string) ([]Process, error) {
	var out []Process
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := processRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("process line %q must look like \"worker: command\" or \"worker x2: command\"", line)
		}
		if m[1] == "web" {
			return nil, errors.New("\"web\" is the start command; name extra processes differently")
		}
		if seen[m[1]] {
			return nil, fmt.Errorf("process %q is listed twice", m[1])
		}
		seen[m[1]] = true
		n := 1
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
			if n < 1 || n > 20 {
				return nil, fmt.Errorf("process %q: instance count must be 1-20", m[1])
			}
		}
		out = append(out, Process{Name: m[1], Count: n, Cmd: strings.TrimSpace(m[3])})
	}
	return out, nil
}

func processContainer(a *App, p Process, i int, release int64) string {
	return fmt.Sprintf("islet-%s-%s-%d-r%d", a.Name, p.Name, i, release)
}

// removeProcesses deletes the app's process containers whose name carries
// the given release (matching=true) or any other release (matching=false).
func (s *Service) removeProcesses(ctx context.Context, a *App, release int64, matching bool) {
	out, err := s.run.Run(ctx, "deploy", "docker", "ps", "-a", "--filter", "label=islet.app="+a.Name, "--filter", "label=islet.process", "--format", "{{.Names}}")
	if err != nil {
		return
	}
	suffix := fmt.Sprintf("-r%d", release)
	for _, n := range strings.Fields(out.Stdout) {
		if strings.HasSuffix(n, suffix) == matching {
			_, _ = s.run.Run(ctx, "deploy", "docker", "rm", "-f", n)
		}
	}
}

// HostPath is one routed entry of an app: a host and an optional path prefix.
type HostPath struct {
	Host   string
	Prefix string
}

// HostPaths splits the domain list ("example.com, example.com/docs").
func (a *App) HostPaths() []HostPath {
	var out []HostPath
	for _, h := range a.Domains() {
		host, prefix, _ := strings.Cut(h, "/")
		if prefix != "" {
			prefix = "/" + prefix
		}
		out = append(out, HostPath{Host: host, Prefix: prefix})
	}
	return out
}

// Promote deploys the image that is live on app from onto app to, without a
// rebuild. The image is re-tagged for the target so each app owns its tags.
func (s *Service) Promote(ctx context.Context, actor, from, to string) (*Release, error) {
	src, err := s.Get(ctx, from)
	if err != nil {
		return nil, err
	}
	if from == to {
		return nil, errors.New("pick a different app to promote to")
	}
	if src.CurrentRelease == 0 {
		return nil, errors.New("nothing is live on " + src.Name + " yet")
	}
	if src.Strategy == "compose" {
		return nil, errors.New("Compose apps cannot be promoted; deploy the target from the same branch")
	}
	rel, err := s.Release(ctx, src.ID, src.CurrentRelease)
	if err != nil || rel.Image == "" {
		return nil, errors.New("the live release of " + src.Name + " has no image")
	}
	dst, err := s.Get(ctx, to)
	if err != nil {
		return nil, err
	}
	if dst.Strategy == "compose" {
		return nil, errors.New("Compose apps cannot receive a promoted image")
	}
	cp := *rel
	cp.Message = "promoted from " + src.Name + " #" + strconv.Itoa(rel.Number) + ": " + rel.Message
	return s.start(ctx, actor, to, "promote", 0, "", &cp)
}
