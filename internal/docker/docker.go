// Package docker manages containers, images, volumes, networks and Compose
// stacks through the Docker CLI. Using the CLI rather than the SDK keeps the
// binary small, makes every action a command the user can read in the
// transparency drawer, and gives Compose for free.
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,199}$`)

// ErrBadName rejects identifiers that could be mistaken for flags.
var ErrBadName = errors.New("invalid name")

// ValidName reports whether n is safe to pass to docker or systemd.
func ValidName(n string) bool { return nameRe.MatchString(n) }

func checkName(n string) error {
	if !nameRe.MatchString(n) {
		return ErrBadName
	}
	return nil
}

// Service is the Docker facade.
type Service struct {
	run       *cmdrun.Runner
	stacksDir string
}

// New builds the service. stacksDir holds Compose files for managed stacks.
func New(run *cmdrun.Runner, stacksDir string) *Service {
	return &Service{run: run, stacksDir: stacksDir}
}

// Status reports whether Docker is usable and which version runs.
type Status struct {
	Available      bool   `json:"available"`
	Version        string `json:"version"`
	ComposeVersion string `json:"composeVersion"`
	Error          string `json:"error,omitempty"`
}

// Status probes the daemon.
func (s *Service) Status(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := s.run.Run(ctx, "system", "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return Status{Available: false, Error: err.Error()}
	}
	st := Status{Available: true, Version: strings.TrimSpace(res.Stdout)}
	if r2, err := s.run.Run(ctx, "system", "docker", "compose", "version", "--short"); err == nil {
		st.ComposeVersion = strings.TrimSpace(r2.Stdout)
	}
	return st
}

// ---- containers ----

// Container is one row of the containers list.
type Container struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Image     string   `json:"image"`
	State     string   `json:"state"`  // running, exited, paused, restarting, created
	Status    string   `json:"status"` // human text from docker ps
	Ports     string   `json:"ports"`
	CreatedAt string   `json:"createdAt"`
	Stack     string   `json:"stack,omitempty"`   // compose project
	Service   string   `json:"service,omitempty"` // compose service
	CPUPct    float64  `json:"cpuPct"`
	MemUsage  string   `json:"memUsage"`
	MemPct    float64  `json:"memPct"`
	NetIO     string   `json:"netIO"`
	Labels    []string `json:"-"`
}

type psRow struct {
	ID, Names, Image, State, Status, Ports, CreatedAt, Labels string
}

type statsRow struct {
	ID, Name, CPUPerc, MemUsage, MemPerc, NetIO string
}

// Containers lists every container with live stats for the running ones.
func (s *Service) Containers(ctx context.Context, actor string) ([]Container, error) {
	res, err := s.run.Run(ctx, actor, "docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	stats := map[string]statsRow{}
	if r2, err := s.run.Run(ctx, actor, "docker", "stats", "--no-stream", "--format", "{{json .}}"); err == nil {
		for _, line := range lines(r2.Stdout) {
			var st statsRow
			if json.Unmarshal([]byte(line), &st) == nil {
				stats[st.Name] = st
			}
		}
	}
	out := []Container{}
	for _, line := range lines(res.Stdout) {
		var p psRow
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue
		}
		c := Container{ID: p.ID, Name: p.Names, Image: p.Image, State: p.State, Status: p.Status, Ports: p.Ports, CreatedAt: p.CreatedAt}
		if len(c.ID) > 12 {
			c.ID = c.ID[:12]
		}
		for _, l := range strings.Split(p.Labels, ",") {
			k, v, _ := strings.Cut(l, "=")
			switch k {
			case "com.docker.compose.project":
				c.Stack = v
			case "com.docker.compose.service":
				c.Service = v
			}
		}
		if st, ok := stats[c.Name]; ok {
			c.CPUPct = parsePct(st.CPUPerc)
			c.MemUsage = st.MemUsage
			c.MemPct = parsePct(st.MemPerc)
			c.NetIO = st.NetIO
		}
		out = append(out, c)
	}
	return out, nil
}

// Inspect is the detail view of a container.
type Inspect struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	State         string            `json:"state"`
	StartedAt     string            `json:"startedAt"`
	RestartCount  int               `json:"restartCount"`
	RestartPolicy string            `json:"restartPolicy"`
	Cmd           []string          `json:"cmd"`
	Env           []string          `json:"env"`
	Mounts        []Mount           `json:"mounts"`
	Ports         map[string]string `json:"ports"`
	Labels        map[string]string `json:"labels"`
	MemoryLimit   int64             `json:"memoryLimit"`
	CPULimit      float64           `json:"cpuLimit"`
	Networks      []string          `json:"networks"`
	Stack         string            `json:"stack,omitempty"`
	Service       string            `json:"service,omitempty"`
}

// Mount is a volume or bind mount.
type Mount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

// Inspect returns details for one container. Env values are returned as-is;
// the API layer masks secrets for non-admins.
func (s *Service) Inspect(ctx context.Context, actor, id string) (*Inspect, error) {
	if err := checkName(id); err != nil {
		return nil, err
	}
	res, err := s.run.Run(ctx, actor, "docker", "inspect", "--type", "container", id)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID           string `json:"Id"`
		Name         string
		Created      string
		RestartCount int
		State        struct{ Status, StartedAt string }
		Config       struct {
			Image  string
			Cmd    []string
			Env    []string
			Labels map[string]string
		}
		HostConfig struct {
			RestartPolicy struct{ Name string }
			Memory        int64
			NanoCpus      int64
		}
		Mounts []struct {
			Type, Source, Destination string
			RW                        bool
		}
		NetworkSettings struct {
			Ports    map[string][]struct{ HostIp, HostPort string }
			Networks map[string]any
		}
	}
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("inspect: bad output")
	}
	r := raw[0]
	out := &Inspect{ID: r.ID[:12], Name: strings.TrimPrefix(r.Name, "/"), Image: r.Config.Image, State: r.State.Status, StartedAt: r.State.StartedAt,
		RestartCount: r.RestartCount, RestartPolicy: r.HostConfig.RestartPolicy.Name, Cmd: r.Config.Cmd, Env: r.Config.Env, Labels: r.Config.Labels,
		MemoryLimit: r.HostConfig.Memory, CPULimit: float64(r.HostConfig.NanoCpus) / 1e9, Ports: map[string]string{}}
	for _, m := range r.Mounts {
		out.Mounts = append(out.Mounts, Mount{Type: m.Type, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	for port, binds := range r.NetworkSettings.Ports {
		var hs []string
		for _, b := range binds {
			hs = append(hs, b.HostIp+":"+b.HostPort)
		}
		out.Ports[port] = strings.Join(hs, ", ")
	}
	for n := range r.NetworkSettings.Networks {
		out.Networks = append(out.Networks, n)
	}
	out.Stack = r.Config.Labels["com.docker.compose.project"]
	out.Service = r.Config.Labels["com.docker.compose.service"]
	return out, nil
}

// ContainerAction runs start, stop, restart, pause, unpause, kill or remove.
func (s *Service) ContainerAction(ctx context.Context, actor, id, action string) error {
	if err := checkName(id); err != nil {
		return err
	}
	var args []string
	switch action {
	case "start", "stop", "restart", "pause", "unpause", "kill":
		args = []string{action, id}
	case "remove":
		args = []string{"rm", "-f", id}
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	_, err := s.run.Run(ctx, actor, "docker", args...)
	return err
}

// UpdateLimits sets memory (bytes, 0 = unlimited) and CPUs (0 = unlimited).
func (s *Service) UpdateLimits(ctx context.Context, actor, id string, memory int64, cpus float64, restart string) error {
	if err := checkName(id); err != nil {
		return err
	}
	args := []string{"update"}
	args = append(args, "--memory", fmt.Sprint(memory), "--memory-swap", fmt.Sprint(memory))
	if memory == 0 {
		args = []string{"update", "--memory", "0", "--memory-swap", "-1"}
	}
	args = append(args, "--cpus", fmt.Sprintf("%g", cpus))
	if restart != "" {
		if _, ok := map[string]bool{"no": true, "always": true, "unless-stopped": true, "on-failure": true}[restart]; !ok {
			return errors.New("restart policy must be no, always, unless-stopped or on-failure")
		}
		args = append(args, "--restart", restart)
	}
	args = append(args, id)
	_, err := s.run.Run(ctx, actor, "docker", args...)
	return err
}

// Logs streams container logs. The reader yields raw log lines.
func (s *Service) Logs(ctx context.Context, actor, id string, tail int, follow bool) (io.ReadCloser, func() error, error) {
	if err := checkName(id); err != nil {
		return nil, nil, err
	}
	args := []string{"logs", "--timestamps", "--tail", fmt.Sprint(tail)}
	if follow {
		args = append(args, "--follow")
	}
	args = append(args, id)
	return s.run.Stream(ctx, actor, "docker", args...)
}

// ExecCommand returns the argv for an interactive shell inside a container.
func ExecCommand(id string) ([]string, error) {
	if err := checkName(id); err != nil {
		return nil, err
	}
	return []string{"docker", "exec", "-it", id, "sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}, nil
}

// ---- images ----

// Image is one image row.
type Image struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Size       string `json:"size"`
	CreatedAt  string `json:"createdAt"`
	Dangling   bool   `json:"dangling"`
	InUse      bool   `json:"inUse"`
}

// Images lists images and marks the ones no container uses.
func (s *Service) Images(ctx context.Context, actor string) ([]Image, error) {
	res, err := s.run.Run(ctx, actor, "docker", "images", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	if r2, err := s.run.Run(ctx, actor, "docker", "ps", "-a", "--format", "{{.Image}}"); err == nil {
		for _, l := range lines(r2.Stdout) {
			used[l] = true
		}
	}
	out := []Image{}
	for _, line := range lines(res.Stdout) {
		var r struct{ ID, Repository, Tag, Size, CreatedAt string }
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		img := Image{ID: strings.TrimPrefix(r.ID, "sha256:"), Repository: r.Repository, Tag: r.Tag, Size: r.Size, CreatedAt: r.CreatedAt}
		img.Dangling = r.Repository == "<none>"
		ref := r.Repository + ":" + r.Tag
		img.InUse = used[ref] || used[r.Repository] || used[img.ID]
		out = append(out, img)
	}
	return out, nil
}

// Pull pulls an image, streaming progress lines.
func (s *Service) Pull(ctx context.Context, actor, ref string) (io.ReadCloser, func() error, error) {
	if err := checkName(ref); err != nil {
		return nil, nil, err
	}
	return s.run.Stream(ctx, actor, "docker", "pull", ref)
}

// RemoveImage deletes an image.
func (s *Service) RemoveImage(ctx context.Context, actor, id string, force bool) error {
	if err := checkName(id); err != nil {
		return err
	}
	args := []string{"rmi"}
	if force {
		args = append(args, "-f")
	}
	_, err := s.run.Run(ctx, actor, "docker", append(args, id)...)
	return err
}

// ---- volumes and networks ----

// Volume is one named volume.
type Volume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
	Size       string `json:"size"`
	InUse      bool   `json:"inUse"`
	Stack      string `json:"stack,omitempty"`
}

// Volumes lists volumes with size and usage.
func (s *Service) Volumes(ctx context.Context, actor string) ([]Volume, error) {
	res, err := s.run.Run(ctx, actor, "docker", "volume", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	sizes := map[string]string{}
	inUse := map[string]bool{}
	if r2, err := s.run.Run(ctx, actor, "docker", "system", "df", "-v", "--format", "{{json .}}"); err == nil {
		var df struct {
			Volumes []struct {
				Name, Size string
				Links      int
			}
		}
		if json.Unmarshal([]byte(r2.Stdout), &df) == nil {
			for _, v := range df.Volumes {
				sizes[v.Name] = v.Size
				inUse[v.Name] = v.Links > 0
			}
		}
	}
	out := []Volume{}
	for _, line := range lines(res.Stdout) {
		var r struct{ Name, Driver, Mountpoint, Labels string }
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		v := Volume{Name: r.Name, Driver: r.Driver, Mountpoint: r.Mountpoint, Size: sizes[r.Name], InUse: inUse[r.Name]}
		for _, l := range strings.Split(r.Labels, ",") {
			if k, val, _ := strings.Cut(l, "="); k == "com.docker.compose.project" {
				v.Stack = val
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// RemoveVolume deletes a volume. Docker refuses while a container uses it.
func (s *Service) RemoveVolume(ctx context.Context, actor, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	_, err := s.run.Run(ctx, actor, "docker", "volume", "rm", name)
	return err
}

// Network is one network row.
type Network struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

// Networks lists networks.
func (s *Service) Networks(ctx context.Context, actor string) ([]Network, error) {
	res, err := s.run.Run(ctx, actor, "docker", "network", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	out := []Network{}
	for _, line := range lines(res.Stdout) {
		var r struct{ ID, Name, Driver, Scope string }
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, Network{ID: r.ID, Name: r.Name, Driver: r.Driver, Scope: r.Scope})
		}
	}
	return out, nil
}

// RemoveNetwork deletes a network.
func (s *Service) RemoveNetwork(ctx context.Context, actor, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	_, err := s.run.Run(ctx, actor, "docker", "network", "rm", name)
	return err
}

// ---- disk usage and prune ----

// DiskUsage is one row of docker system df.
type DiskUsage struct {
	Type        string `json:"type"`
	Total       int    `json:"total"`
	Active      int    `json:"active"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

// SystemDF reports what Docker uses on disk.
func (s *Service) SystemDF(ctx context.Context, actor string) ([]DiskUsage, error) {
	res, err := s.run.Run(ctx, actor, "docker", "system", "df", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	out := []DiskUsage{}
	for _, line := range lines(res.Stdout) {
		var r struct {
			Type, Size, Reclaimable string
			TotalCount, Active      string
		}
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, DiskUsage{Type: r.Type, Total: atoi(r.TotalCount), Active: atoi(r.Active), Size: r.Size, Reclaimable: r.Reclaimable})
		}
	}
	return out, nil
}

// PruneOptions selects what to remove.
type PruneOptions struct {
	Containers bool `json:"containers"` // stopped containers
	Images     bool `json:"images"`     // dangling images
	AllImages  bool `json:"allImages"`  // every unused image
	Volumes    bool `json:"volumes"`    // unused volumes (data loss!)
	Networks   bool `json:"networks"`
	Builder    bool `json:"builder"`
}

// Prune removes the selected unused objects and returns Docker's summary.
func (s *Service) Prune(ctx context.Context, actor string, o PruneOptions) (map[string]string, error) {
	out := map[string]string{}
	run := func(key string, args ...string) error {
		res, err := s.run.Run(ctx, actor, "docker", args...)
		if err != nil {
			return err
		}
		out[key] = reclaimed(res.Stdout)
		return nil
	}
	if o.Containers {
		if err := run("containers", "container", "prune", "-f"); err != nil {
			return out, err
		}
	}
	if o.Images || o.AllImages {
		args := []string{"image", "prune", "-f"}
		if o.AllImages {
			args = append(args, "-a")
		}
		if err := run("images", args...); err != nil {
			return out, err
		}
	}
	if o.Volumes {
		if err := run("volumes", "volume", "prune", "-f", "-a"); err != nil {
			return out, err
		}
	}
	if o.Networks {
		if err := run("networks", "network", "prune", "-f"); err != nil {
			return out, err
		}
	}
	if o.Builder {
		if err := run("builder", "builder", "prune", "-f"); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ---- compose stacks ----

var stackNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Stack is a managed Compose project.
type Stack struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Managed   bool   `json:"managed"` // has a file under stacksDir
	Status    string `json:"status"`  // from docker compose ls
	Services  int    `json:"services"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	App       string `json:"app,omitempty"` // catalog slug when installed from the catalog
}

func (s *Service) stackFile(name string) (string, error) {
	if !stackNameRe.MatchString(name) {
		return "", errors.New("stack name must be lowercase letters, digits, dash or underscore")
	}
	return filepath.Join(s.stacksDir, name, "compose.yaml"), nil
}

// Stacks lists managed stacks and any other Compose projects Docker knows.
func (s *Service) Stacks(ctx context.Context, actor string) ([]Stack, error) {
	byName := map[string]*Stack{}
	entries, _ := os.ReadDir(s.stacksDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(s.stacksDir, e.Name(), "compose.yaml")
		if st, err := os.Stat(p); err == nil {
			sk := &Stack{Name: e.Name(), Path: p, Managed: true, Status: "down", UpdatedAt: st.ModTime().UTC().Format(time.RFC3339)}
			if b, err := os.ReadFile(filepath.Join(s.stacksDir, e.Name(), "islet-app.json")); err == nil {
				var meta struct{ Slug string }
				if json.Unmarshal(b, &meta) == nil {
					sk.App = meta.Slug
				}
			}
			byName[e.Name()] = sk
		}
	}
	if res, err := s.run.Run(ctx, actor, "docker", "compose", "ls", "-a", "--format", "json"); err == nil {
		var rows []struct{ Name, Status, ConfigFiles string }
		if json.Unmarshal([]byte(res.Stdout), &rows) == nil {
			for _, r := range rows {
				st, ok := byName[r.Name]
				if !ok {
					st = &Stack{Name: r.Name, Path: r.ConfigFiles}
					byName[r.Name] = st
				}
				st.Status = r.Status
				if n, ok := strings.CutPrefix(r.Status, "running("); ok {
					st.Services = atoi(strings.TrimSuffix(n, ")"))
				}
			}
		}
	}
	out := make([]Stack, 0, len(byName))
	for _, st := range byName {
		out = append(out, *st)
	}
	sortStacks(out)
	return out, nil
}

// ReadStack returns the Compose file and env of a managed stack.
func (s *Service) ReadStack(name string) (compose, env string, err error) {
	p, err := s.stackFile(name)
	if err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", "", err
	}
	e, _ := os.ReadFile(filepath.Join(filepath.Dir(p), ".env"))
	return string(b), string(e), nil
}

// WriteStack validates and saves a Compose file (and optional .env).
func (s *Service) WriteStack(ctx context.Context, actor, name, compose, env string) error {
	p, err := s.stackFile(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(compose) == "" {
		return errors.New("compose file is empty")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(p+".tmp", []byte(compose), 0o640); err != nil {
		return err
	}
	if env != "" {
		if err := os.WriteFile(filepath.Join(filepath.Dir(p), ".env"), []byte(env), 0o600); err != nil {
			return err
		}
	}
	if _, err := s.run.Run(ctx, actor, "docker", "compose", "-p", name, "-f", p+".tmp", "config", "-q"); err != nil {
		os.Remove(p + ".tmp")
		return fmt.Errorf("compose file is not valid: %w", err)
	}
	return os.Rename(p+".tmp", p)
}

// StackAction runs up, down, pull, restart or remove (down plus delete files).
func (s *Service) StackAction(ctx context.Context, actor, name, action string) (io.ReadCloser, func() error, error) {
	p, err := s.stackFile(name)
	if err != nil {
		return nil, nil, err
	}
	base := []string{"compose", "-p", name, "-f", p}
	var args []string
	switch action {
	case "up":
		args = append(base, "up", "-d", "--remove-orphans")
	case "down":
		args = append(base, "down")
	case "pull":
		args = append(base, "pull")
	case "update":
		args = append(base, "up", "-d", "--pull", "always", "--remove-orphans")
	case "restart":
		args = append(base, "restart")
	default:
		return nil, nil, fmt.Errorf("unknown action %q", action)
	}
	return s.run.Stream(ctx, actor, "docker", args...)
}

// RemoveStack takes a stack down and deletes its files. Volumes are kept
// unless removeVolumes is set.
func (s *Service) RemoveStack(ctx context.Context, actor, name string, removeVolumes bool) error {
	p, err := s.stackFile(name)
	if err != nil {
		return err
	}
	args := []string{"compose", "-p", name, "-f", p, "down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "-v")
	}
	if _, err := os.Stat(p); err == nil {
		if _, err := s.run.Run(ctx, actor, "docker", args...); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Dir(p))
}

// ---- helpers ----

func lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func parsePct(s string) float64 {
	var v float64
	fmt.Sscanf(strings.TrimSuffix(strings.TrimSpace(s), "%"), "%f", &v)
	return v
}

func atoi(s string) int {
	var n int
	fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n
}

func reclaimed(out string) string {
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "Total reclaimed space:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "Total reclaimed space:"))
		}
	}
	return "0B"
}

func sortStacks(s []Stack) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Name < s[j-1].Name; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
