// Package deploy turns a git repository or an image into a running,
// routed container: clone, detect, build, pre-deploy, health check, switch
// the proxy, drain the old version. Releases are kept so a rollback is a
// re-point, not a rebuild.
package deploy

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/store"
)

// App is a deployable application.
type App struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Source         string  `json:"source"` // git | image
	RepoURL        string  `json:"repoUrl"`
	Branch         string  `json:"branch"`
	RootDir        string  `json:"rootDir"`
	Image          string  `json:"image"`
	Strategy       string  `json:"strategy"`
	Framework      string  `json:"framework"`
	InstallCmd     string  `json:"installCmd"`
	BuildCmd       string  `json:"buildCmd"`
	StartCmd       string  `json:"startCmd"`
	OutputDir      string  `json:"outputDir"`
	Port           int     `json:"port"`
	HealthPath     string  `json:"healthPath"`
	PredeployCmd   string  `json:"predeployCmd"`
	Env            string  `json:"env"` // KEY=VALUE lines, masked for non-admins
	Domain         string  `json:"domain"`
	TLS            string  `json:"tls"`
	WebhookSecret  string  `json:"webhookSecret,omitempty"`
	AutoDeploy     bool    `json:"autoDeploy"`
	MemoryMB       int     `json:"memoryMb"`
	CPUs           float64 `json:"cpus"`
	Volumes        string  `json:"volumes"`
	CurrentRelease int64   `json:"currentRelease"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	NodeVer        string  `json:"nodeVersion"`
	PyVer          string  `json:"pythonVersion"`

	// Derived
	URL       string   `json:"url"`
	Container string   `json:"container"`
	Deploying bool     `json:"deploying"`
	Last      *Release `json:"lastRelease,omitempty"`
}

// Release is one deploy attempt.
type Release struct {
	ID         int64  `json:"id"`
	AppID      string `json:"appId"`
	Number     int    `json:"number"`
	Trigger    string `json:"trigger"`
	Actor      string `json:"actor"`
	Commit     string `json:"commit"`
	Message    string `json:"message"`
	Author     string `json:"author"`
	Status     string `json:"status"`
	Image      string `json:"image"`
	Container  string `json:"container"`
	Log        string `json:"log,omitempty"`
	Error      string `json:"error"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	DurationMs int64  `json:"durationMs"`
}

// ErrNotFound is returned for unknown apps or releases.
var ErrNotFound = errors.New("app not found")

// ErrBusy is returned when a deploy is already running for the app.
var ErrBusy = errors.New("a deploy is already running for this app")

const sqlTime = "2006-01-02T15:04:05.000Z"

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var validStrategies = map[string]bool{"auto": true, "static": true, "node": true, "python": true, "go": true, "dockerfile": true, "compose": true, "image": true}

// Service is the deploy engine.
type Service struct {
	st      *store.Store
	keys    *auth.Keys
	run     *cmdrun.Runner
	dk      *docker.Service
	px      *proxy.Manager
	bus     *notify.Bus
	log     *slog.Logger
	dir     string
	mu      sync.Mutex
	active  map[string]*run
	dataDir string

	// CloneAuth may return an authenticated clone URL for a repository (GitHub App).
	CloneAuth func(ctx context.Context, repoURL string) (string, bool)
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}
	subs   []chan string
	mu     sync.Mutex
	lines  []string
}

// New builds the service. Workspaces live under <data>/apps/<id>.
func New(st *store.Store, keys *auth.Keys, runner *cmdrun.Runner, dk *docker.Service, px *proxy.Manager, bus *notify.Bus, dataDir string, log *slog.Logger) *Service {
	abs, _ := filepath.Abs(dataDir)
	return &Service{st: st, keys: keys, run: runner, dk: dk, px: px, bus: bus, log: log, dir: filepath.Join(abs, "apps"), active: map[string]*run{}, dataDir: abs}
}

// Start marks releases interrupted by a restart as failed.
func (s *Service) Start(ctx context.Context) error {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return err
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE releases SET status = 'failed', error = 'daemon restarted during the deploy', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE status IN ('queued','building','deploying')`)
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE apps SET status = 'failed' WHERE status = 'building'`)
	return nil
}

// ---- validation ----

// Validate normalises an app before saving.
func (a *App) Validate() error {
	a.Name = strings.ToLower(strings.TrimSpace(a.Name))
	if !nameRe.MatchString(a.Name) {
		return errors.New("name must be 1-40 lowercase letters, digits or dashes")
	}
	switch a.Source {
	case "git":
		u, err := url.Parse(strings.TrimSpace(a.RepoURL))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" && u.Scheme != "file" && !strings.HasPrefix(a.RepoURL, "git@")) {
			return errors.New("repository must be an https://, ssh:// or file:// URL")
		}
		if a.Branch == "" {
			a.Branch = "main"
		}
		if strings.ContainsAny(a.Branch, " \t\n'\"`$") || strings.HasPrefix(a.Branch, "-") {
			return errors.New("invalid branch name")
		}
	case "image":
		if !docker.ValidName(a.Image) {
			return errors.New("image must be a valid reference like ghcr.io/org/app:1.2")
		}
		a.Strategy = "image"
	default:
		return errors.New("source must be git or image")
	}
	if a.Strategy == "" {
		a.Strategy = "auto"
	}
	if !validStrategies[a.Strategy] {
		return errors.New("unknown strategy")
	}
	if a.Strategy == "static" {
		a.Port = 80
	}
	a.RootDir = strings.Trim(strings.TrimSpace(a.RootDir), "/")
	if strings.Contains(a.RootDir, "..") {
		return errors.New("root directory cannot leave the repository")
	}
	if a.Port < 0 || a.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if a.HealthPath == "" {
		a.HealthPath = "/"
	}
	if !strings.HasPrefix(a.HealthPath, "/") {
		return errors.New("health path must start with /")
	}
	switch a.TLS {
	case "", "letsencrypt":
		a.TLS = "letsencrypt"
	case "self", "none":
	default:
		return errors.New("tls must be letsencrypt, self or none")
	}
	var hosts []string
	seen := map[string]bool{}
	for _, h := range strings.Split(a.Domain, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		if strings.ContainsAny(h, " /:") {
			return fmt.Errorf("domain %q is not a host name", h)
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	a.Domain = strings.Join(hosts, ",")
	for _, kv := range strings.Split(a.Env, "\n") {
		kv = strings.TrimSpace(kv)
		if kv == "" || strings.HasPrefix(kv, "#") {
			continue
		}
		k, _, ok := strings.Cut(kv, "=")
		if !ok || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) {
			return fmt.Errorf("environment line %q is not KEY=VALUE", kv)
		}
	}
	if a.MemoryMB < 0 || a.CPUs < 0 {
		return errors.New("resource limits cannot be negative")
	}
	for _, v := range strings.Split(a.Volumes, "\n") {
		v = strings.TrimSpace(v)
		if v != "" && !strings.HasPrefix(v, "/") {
			return errors.New("volume paths must be absolute container paths")
		}
	}
	return nil
}

// ---- CRUD ----

const cols = `id, name, source, repo_url, branch, root_dir, image, strategy, framework, install_cmd, build_cmd, start_cmd, output_dir, port, health_path, predeploy_cmd, env, domain, tls, webhook_secret, auto_deploy, memory_mb, cpus, volumes, current_release, status, created_at, updated_at`

func (s *Service) scan(sc interface{ Scan(...any) error }) (App, error) {
	var a App
	var repo, env []byte
	err := sc.Scan(&a.ID, &a.Name, &a.Source, &repo, &a.Branch, &a.RootDir, &a.Image, &a.Strategy, &a.Framework, &a.InstallCmd, &a.BuildCmd, &a.StartCmd, &a.OutputDir, &a.Port, &a.HealthPath, &a.PredeployCmd, &env, &a.Domain, &a.TLS, &a.WebhookSecret, &a.AutoDeploy, &a.MemoryMB, &a.CPUs, &a.Volumes, &a.CurrentRelease, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	a.RepoURL = s.open(repo)
	a.Env = s.open(env)
	// Node/Python versions ride along in env as ISLET_NODE_VERSION / ISLET_PYTHON_VERSION.
	for _, kv := range strings.Split(a.Env, "\n") {
		if v, ok := strings.CutPrefix(kv, "ISLET_NODE_VERSION="); ok {
			a.NodeVer = v
		}
		if v, ok := strings.CutPrefix(kv, "ISLET_PYTHON_VERSION="); ok {
			a.PyVer = v
		}
	}
	return a, nil
}

func (s *Service) seal(v string) []byte {
	if v == "" {
		return []byte{}
	}
	b, err := s.keys.Encrypt([]byte(v))
	if err != nil {
		return []byte{}
	}
	return b
}

func (s *Service) open(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	p, err := s.keys.Decrypt(b)
	if err != nil {
		return ""
	}
	return string(p)
}

func (s *Service) decorate(ctx context.Context, a *App) {
	a.Container = a.containerName(a.CurrentRelease)
	if hosts := a.Domains(); len(hosts) > 0 {
		scheme := "https"
		if a.TLS == "none" {
			scheme = "http"
		}
		a.URL = scheme + "://" + hosts[0]
	}
	s.mu.Lock()
	_, a.Deploying = s.active[a.ID]
	s.mu.Unlock()
	if r, err := s.lastRelease(ctx, a.ID); err == nil {
		a.Last = r
	}
}

// Domains returns the app's hosts; the first is the primary one.
func (a *App) Domains() []string {
	if a.Domain == "" {
		return nil
	}
	return strings.Split(a.Domain, ",")
}

func (a *App) containerName(release int64) string {
	if release == 0 {
		return ""
	}
	return fmt.Sprintf("islet-%s-r%d", a.Name, release)
}

// List returns every app.
func (s *Service) List(ctx context.Context) ([]App, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM apps WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		s.decorate(ctx, &a)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Get returns one app by id.
func (s *Service) Get(ctx context.Context, id string) (*App, error) {
	a, err := s.scan(s.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM apps WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.decorate(ctx, &a)
	return &a, nil
}

// Save inserts or updates an app.
func (s *Service) Save(ctx context.Context, a *App) (*App, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if a.ID == "" {
		a.ID = randHex(6)
		a.WebhookSecret = randHex(24)
		if a.Domain == "" {
			a.Domain = proxy.PreviewHost(ctx, a.Name)
		}
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO apps (id, server_id, name, source, repo_url, branch, root_dir, image, strategy, framework, install_cmd, build_cmd, start_cmd, output_dir, port, health_path, predeploy_cmd, env, domain, tls, webhook_secret, auto_deploy, memory_mb, cpus, volumes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, s.st.ServerID, a.Name, a.Source, s.seal(a.RepoURL), a.Branch, a.RootDir, a.Image, a.Strategy, a.Framework, a.InstallCmd, a.BuildCmd, a.StartCmd, a.OutputDir, a.Port, a.HealthPath, a.PredeployCmd, s.seal(a.Env), a.Domain, a.TLS, a.WebhookSecret, a.AutoDeploy, a.MemoryMB, a.CPUs, a.Volumes)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("an app with that name already exists")
			}
			return nil, err
		}
	} else {
		old, err := s.Get(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if a.Name != old.Name {
			return nil, errors.New("apps cannot be renamed; create a new one")
		}
		_, err = s.st.DB.ExecContext(ctx, `UPDATE apps SET source = ?, repo_url = ?, branch = ?, root_dir = ?, image = ?, strategy = ?, framework = ?, install_cmd = ?, build_cmd = ?, start_cmd = ?, output_dir = ?, port = ?, health_path = ?, predeploy_cmd = ?, env = ?, domain = ?, tls = ?, auto_deploy = ?, memory_mb = ?, cpus = ?, volumes = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
			a.Source, s.seal(a.RepoURL), a.Branch, a.RootDir, a.Image, a.Strategy, a.Framework, a.InstallCmd, a.BuildCmd, a.StartCmd, a.OutputDir, a.Port, a.HealthPath, a.PredeployCmd, s.seal(a.Env), a.Domain, a.TLS, a.AutoDeploy, a.MemoryMB, a.CPUs, a.Volumes, a.ID)
		if err != nil {
			return nil, err
		}
		// A changed domain re-points the route to the live container.
		if old.Domain != a.Domain {
			keep := map[string]bool{}
			for _, h := range a.Domains() {
				keep[h] = true
			}
			for _, h := range old.Domains() {
				if !keep[h] {
					s.removeDomain(ctx, h)
				}
			}
			if a.Domain != "" && old.CurrentRelease != 0 {
				_ = s.route(ctx, "system", a, old.containerName(old.CurrentRelease))
			}
		}
	}
	return s.Get(ctx, a.ID)
}

// Delete stops containers, removes images, the route and the workspace.
func (s *Service) Delete(ctx context.Context, actor, id string) error {
	a, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	s.Cancel(id)
	rels, _ := s.Releases(ctx, id, 500)
	for _, r := range rels {
		if r.Container != "" {
			_, _ = s.run.Run(ctx, actor, "docker", "rm", "-f", r.Container)
		}
		if r.Image != "" && a.Source != "image" {
			_, _ = s.run.Run(ctx, actor, "docker", "rmi", "-f", r.Image)
		}
	}
	if a.Strategy == "compose" {
		_ = s.dk.RemoveStack(ctx, actor, "app-"+a.Name, false)
	}
	for _, h := range a.Domains() {
		s.removeDomain(ctx, h)
	}
	_ = os.RemoveAll(filepath.Join(s.dir, a.ID))
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM releases WHERE app_id = ?`, id)
	_, err = s.st.DB.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	return err
}

func (s *Service) removeDomain(ctx context.Context, host string) {
	doms, err := s.px.Domains(ctx)
	if err != nil {
		return
	}
	for _, d := range doms {
		if d.Host == host {
			_ = s.px.Delete(ctx, "system", d.ID)
		}
	}
}

// Releases lists deploys, newest first, without logs.
func (s *Service) Releases(ctx context.Context, appID string, limit int) ([]Release, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, app_id, number, trigger, actor, commit_sha, message, author, status, image, container, error, started_at, finished_at, duration_ms FROM releases WHERE app_id = ? ORDER BY id DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		var r Release
		if err := rows.Scan(&r.ID, &r.AppID, &r.Number, &r.Trigger, &r.Actor, &r.Commit, &r.Message, &r.Author, &r.Status, &r.Image, &r.Container, &r.Error, &r.StartedAt, &r.FinishedAt, &r.DurationMs); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Service) lastRelease(ctx context.Context, appID string) (*Release, error) {
	list, err := s.Releases(ctx, appID, 1)
	if err != nil || len(list) == 0 {
		return nil, errors.New("none")
	}
	return &list[0], nil
}

// Release returns one release with its log.
func (s *Service) Release(ctx context.Context, appID string, id int64) (*Release, error) {
	var r Release
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, app_id, number, trigger, actor, commit_sha, message, author, status, image, container, log, error, started_at, finished_at, duration_ms FROM releases WHERE app_id = ? AND id = ?`, appID, id).
		Scan(&r.ID, &r.AppID, &r.Number, &r.Trigger, &r.Actor, &r.Commit, &r.Message, &r.Author, &r.Status, &r.Image, &r.Container, &r.Log, &r.Error, &r.StartedAt, &r.FinishedAt, &r.DurationMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// ---- detection ----

// Inspect clones the repository and returns what Islet detected.
func (s *Service) Inspect(ctx context.Context, repoURL, branch, rootDir string) (*Detection, error) {
	tmp, err := os.MkdirTemp(s.dir, "inspect-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := s.clone(ctx, repoURL, branch, tmp, nil); err != nil {
		return nil, err
	}
	d := Detect(filepath.Join(tmp, strings.Trim(rootDir, "/")))
	return &d, nil
}

func (s *Service) clone(ctx context.Context, repoURL, branch, dst string, out io.Writer) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	shown := repoURL
	if s.CloneAuth != nil {
		if u, ok := s.CloneAuth(ctx, repoURL); ok {
			repoURL = u
		}
	}
	cmd := exec.CommandContext(cctx, "git", "clone", "--depth", "1", "--branch", branch, "--single-branch", "--recurse-submodules", "--", repoURL, dst)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new")
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if out != nil {
		cmd.Stderr = io.MultiWriter(&stderr, out)
	}
	start := time.Now()
	err := cmd.Run()
	s.run.Record(ctx, "deploy", "git clone --depth 1 --branch "+branch+" "+redact(shown), cmdrun.Result{ExitCode: code(err), Duration: time.Since(start), Stderr: stderr.String()})
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		msg = strings.ReplaceAll(strings.ReplaceAll(msg, repoURL, redact(shown)), shown, redact(shown))
		return errors.New("git clone failed: " + msg)
	}
	return nil
}

func redact(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.User == nil {
		return u
	}
	p.User = url.User("***")
	return p.String()
}

func code(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// ---- deploying ----

// Deploy starts a release in the background and returns it. Subscribe to
// follow the log.
func (s *Service) Deploy(ctx context.Context, actor, id, trigger string, rollbackTo int64) (*Release, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if _, busy := s.active[id]; busy {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	rctx, cancel := context.WithCancel(context.Background())
	rn := &run{cancel: cancel, done: make(chan struct{})}
	s.active[id] = rn
	s.mu.Unlock()

	var number int
	_ = s.st.DB.QueryRowContext(ctx, `SELECT coalesce(max(number), 0) + 1 FROM releases WHERE app_id = ?`, id).Scan(&number)
	res, err := s.st.DB.ExecContext(ctx, `INSERT INTO releases (app_id, number, trigger, actor, status) VALUES (?, ?, ?, ?, 'queued')`, id, number, trigger, actor)
	if err != nil {
		s.finish(id, rn)
		return nil, err
	}
	rid, _ := res.LastInsertId()
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE apps SET status = 'building' WHERE id = ?`, id)
	rel := &Release{ID: rid, AppID: id, Number: number, Trigger: trigger, Actor: actor, Status: "queued"}
	go func() {
		defer s.finish(id, rn)
		s.pipeline(rctx, a, rel, rn, rollbackTo)
	}()
	return rel, nil
}

func (s *Service) finish(id string, rn *run) {
	s.mu.Lock()
	if s.active[id] == rn {
		delete(s.active, id)
	}
	s.mu.Unlock()
	rn.mu.Lock()
	for _, ch := range rn.subs {
		close(ch)
	}
	rn.subs = nil
	rn.mu.Unlock()
	close(rn.done)
}

// Cancel stops the running deploy of an app.
func (s *Service) Cancel(id string) bool {
	s.mu.Lock()
	rn, ok := s.active[id]
	s.mu.Unlock()
	if !ok {
		return false
	}
	rn.cancel()
	<-rn.done
	return true
}

// Subscribe returns the log so far plus a channel of new lines, or nil
// when nothing is running.
func (s *Service) Subscribe(id string) ([]string, <-chan string, func()) {
	s.mu.Lock()
	rn, ok := s.active[id]
	s.mu.Unlock()
	if !ok {
		return nil, nil, func() {}
	}
	ch := make(chan string, 256)
	rn.mu.Lock()
	past := append([]string(nil), rn.lines...)
	rn.subs = append(rn.subs, ch)
	rn.mu.Unlock()
	return past, ch, func() {
		rn.mu.Lock()
		for i, c := range rn.subs {
			if c == ch {
				rn.subs = append(rn.subs[:i], rn.subs[i+1:]...)
				break
			}
		}
		rn.mu.Unlock()
	}
}

func (rn *run) emit(line string) {
	rn.mu.Lock()
	rn.lines = append(rn.lines, line)
	for _, ch := range rn.subs {
		select {
		case ch <- line:
		default:
		}
	}
	rn.mu.Unlock()
}

type logger struct {
	rn *run
	b  strings.Builder
}

func (l *logger) step(name string) { l.line("── " + name + " ──") }
func (l *logger) line(s string) {
	l.b.WriteString(s + "\n")
	l.rn.emit(s)
}

// Write lets command output flow into the log line by line.
func (l *logger) writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			l.line(sc.Text())
		}
	}()
	return pw
}

func (s *Service) pipeline(ctx context.Context, a *App, rel *Release, rn *run, rollbackTo int64) {
	lg := &logger{rn: rn}
	start := time.Now()
	setStatus := func(st string) {
		_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE releases SET status = ? WHERE id = ?`, st, rel.ID)
	}
	fail := func(err error) {
		msg := err.Error()
		if ctx.Err() != nil {
			msg = "cancelled"
		}
		lg.line("[islet] deploy failed: " + msg)
		st := "failed"
		if ctx.Err() != nil {
			st = "cancelled"
		}
		_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE releases SET status = ?, error = ?, log = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), duration_ms = ? WHERE id = ?`, st, msg, lg.b.String(), time.Since(start).Milliseconds(), rel.ID)
		appStatus := "failed"
		if a.CurrentRelease != 0 {
			appStatus = "live" // the previous release is still serving
		}
		_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE apps SET status = ? WHERE id = ?`, appStatus, a.ID)
		if rel.Container != "" {
			_, _ = s.run.Run(context.Background(), "deploy", "docker", "rm", "-f", rel.Container)
		}
		if s.bus != nil && ctx.Err() == nil {
			tail := lg.b.String()
			if lines := strings.Split(strings.TrimSpace(tail), "\n"); len(lines) > 30 {
				tail = strings.Join(lines[len(lines)-30:], "\n")
			}
			s.bus.Emit(context.Background(), notify.Event{Category: "deploy", Severity: notify.Warning, Title: "Deploy failed: " + a.Name, Message: msg + "\n\n" + tail, Link: "/apps?app=" + a.ID})
		}
	}
	lg.line(fmt.Sprintf("[islet] release #%d of %s (%s)", rel.Number, a.Name, rel.Trigger))
	if s.bus != nil {
		s.bus.Emit(ctx, notify.Event{Category: "deploy", Severity: notify.Info, Title: "Deploy started: " + a.Name, Message: fmt.Sprintf("Release #%d, triggered by %s.", rel.Number, rel.Trigger), Link: "/apps?app=" + a.ID})
	}
	setStatus("building")

	var image string
	env := envLines(a.Env)
	buildEnv, runEnv := splitEnv(env)

	switch {
	case rollbackTo != 0:
		prev, err := s.Release(ctx, a.ID, rollbackTo)
		if err != nil || prev.Image == "" {
			fail(errors.New("that release has no image to roll back to"))
			return
		}
		lg.step("rollback to #" + strconv.Itoa(prev.Number))
		image, rel.Commit, rel.Message, rel.Author = prev.Image, prev.Commit, prev.Message, prev.Author
		lg.line("reusing image " + image)
	case a.Source == "image":
		lg.step("pull " + a.Image)
		if err := s.docker(ctx, lg, "pull", a.Image); err != nil {
			fail(err)
			return
		}
		image = a.Image
		if out, err := s.run.Run(ctx, "deploy", "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", a.Image); err == nil {
			rel.Commit = strings.TrimSpace(out.Stdout)
		}
	default:
		ws := filepath.Join(s.dir, a.ID, "src")
		_ = os.RemoveAll(ws)
		lg.step("clone " + redact(a.RepoURL) + " @ " + a.Branch)
		if err := s.clone(ctx, a.RepoURL, a.Branch, ws, lg.writer()); err != nil {
			fail(err)
			return
		}
		rel.Commit, rel.Message, rel.Author = gitHead(ws)
		lg.line(fmt.Sprintf("commit %s by %s: %s", short(rel.Commit), rel.Author, rel.Message))
		ctxDir := filepath.Join(ws, a.RootDir)
		if a.Strategy == "auto" || a.Framework == "" {
			d := Detect(ctxDir)
			lg.line("detected: " + d.Summary)
			applyDetection(a, d)
			_, _ = s.st.DB.ExecContext(ctx, `UPDATE apps SET strategy = ?, framework = ?, install_cmd = ?, build_cmd = ?, start_cmd = ?, output_dir = ?, port = ? WHERE id = ?`, a.Strategy, a.Framework, a.InstallCmd, a.BuildCmd, a.StartCmd, a.OutputDir, a.Port, a.ID)
		}
		if a.Strategy == "compose" {
			if err := s.deployCompose(ctx, a, rel, lg, ctxDir, runEnv); err != nil {
				fail(err)
				return
			}
			s.succeed(ctx, a, rel, lg, start, "")
			return
		}
		image = fmt.Sprintf("islet/%s:r%d", a.Name, rel.Number)
		if a.Strategy != "dockerfile" {
			if err := os.MkdirAll(filepath.Join(ctxDir, ".islet"), 0o755); err != nil {
				fail(err)
				return
			}
			df := Dockerfile(a, buildEnv)
			if err := os.WriteFile(filepath.Join(ctxDir, ".islet", "Dockerfile"), []byte(df), 0o644); err != nil {
				fail(err)
				return
			}
			if a.Strategy == "static" {
				redirects, _ := os.ReadFile(filepath.Join(ctxDir, "public", "_redirects"))
				if len(redirects) == 0 {
					redirects, _ = os.ReadFile(filepath.Join(ctxDir, "_redirects"))
				}
				_ = os.WriteFile(filepath.Join(ctxDir, ".islet", "nginx.conf"), []byte(NginxConf(true, string(redirects))), 0o644)
				a.Port = 80
			}
			lg.step("build with a generated Dockerfile (" + a.Framework + ")")
			for _, l := range strings.Split(strings.TrimSpace(df), "\n") {
				lg.line("  " + l)
			}
		} else {
			lg.step("build Dockerfile")
		}
		args := []string{"build", "-t", image, "--progress", "plain"}
		if a.Strategy != "dockerfile" {
			args = append(args, "-f", filepath.Join(ctxDir, ".islet", "Dockerfile"))
		}
		for _, kv := range buildEnv {
			args = append(args, "--build-arg", kv)
		}
		if prev := a.containerName(a.CurrentRelease); prev != "" {
			if out, err := s.run.Run(ctx, "deploy", "docker", "inspect", "--format", "{{.Config.Image}}", prev); err == nil {
				args = append(args, "--cache-from", strings.TrimSpace(out.Stdout))
			}
		}
		args = append(args, ctxDir)
		if err := s.docker(ctx, lg, args...); err != nil {
			fail(err)
			return
		}
	}
	rel.Image = image
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE releases SET image = ?, commit_sha = ?, message = ?, author = ? WHERE id = ?`, image, rel.Commit, rel.Message, rel.Author, rel.ID)

	// Environment file for run and pre-deploy.
	envPath := filepath.Join(s.dir, a.ID, "env")
	_ = os.MkdirAll(filepath.Dir(envPath), 0o750)
	if err := os.WriteFile(envPath, []byte(strings.Join(append(runEnv, "PORT="+strconv.Itoa(a.Port)), "\n")+"\n"), 0o600); err != nil {
		fail(err)
		return
	}
	setStatus("deploying")
	if a.PredeployCmd != "" && rollbackTo == 0 {
		lg.step("pre-deploy: " + a.PredeployCmd)
		if err := s.docker(ctx, lg, "run", "--rm", "--env-file", envPath, "--network", proxy.NetworkName, image, "sh", "-c", a.PredeployCmd); err != nil {
			fail(err)
			return
		}
	}
	name := a.containerName(rel.ID)
	rel.Container = name
	lg.step("start " + name)
	args := []string{"run", "-d", "--name", name, "--env-file", envPath, "--network", proxy.NetworkName, "--restart", "unless-stopped",
		"--label", "islet.app=" + a.Name, "--label", "islet.release=" + strconv.Itoa(rel.Number), "--log-opt", "max-size=10m", "--log-opt", "max-file=3"}
	if a.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", a.MemoryMB))
	}
	if a.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(a.CPUs, 'f', -1, 64))
	}
	for _, v := range strings.Split(a.Volumes, "\n") {
		if v = strings.TrimSpace(v); v != "" {
			args = append(args, "-v", fmt.Sprintf("islet-%s-%s:%s", a.Name, strings.Trim(strings.ReplaceAll(v, "/", "-"), "-"), v))
		}
	}
	args = append(args, image)
	if err := s.docker(ctx, lg, args...); err != nil {
		fail(err)
		return
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE releases SET container = ? WHERE id = ?`, name, rel.ID)

	lg.step(fmt.Sprintf("health check http://%s:%d%s", name, a.Port, a.HealthPath))
	if err := s.healthy(ctx, lg, name, a.Port, a.HealthPath); err != nil {
		out, _ := s.run.Run(context.Background(), "deploy", "docker", "logs", "--tail", "40", name)
		for _, l := range strings.Split(strings.TrimSpace(out.Stdout+out.Stderr), "\n") {
			lg.line("  " + l)
		}
		fail(err)
		return
	}
	lg.line("healthy")

	if a.Domain != "" {
		lg.step("route " + strings.Join(a.Domains(), ", ") + " → " + name)
		if err := s.route(ctx, "deploy", a, name); err != nil {
			fail(err)
			return
		}
	}
	// Drain and stop the previous release.
	if old := a.containerName(a.CurrentRelease); old != "" && old != name {
		lg.step("drain " + old)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		_, _ = s.run.Run(context.Background(), "deploy", "docker", "rm", "-f", old)
		_, _ = s.st.DB.ExecContext(ctx, `UPDATE releases SET status = 'superseded' WHERE app_id = ? AND status = 'live'`, a.ID)
	}
	s.succeed(ctx, a, rel, lg, start, name)
	s.pruneImages(context.Background(), a)
}

func (s *Service) succeed(ctx context.Context, a *App, rel *Release, lg *logger, start time.Time, container string) {
	_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE releases SET status = 'superseded' WHERE app_id = ? AND status = 'live' AND id <> ?`, a.ID, rel.ID)
	_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE releases SET status = 'live', container = ?, log = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), duration_ms = ? WHERE id = ?`, container, lg.b.String(), time.Since(start).Milliseconds(), rel.ID)
	_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE apps SET status = 'live', current_release = ? WHERE id = ?`, rel.ID, a.ID)
	url := ""
	if hosts := a.Domains(); len(hosts) > 0 {
		url = "https://" + hosts[0]
		if a.TLS == "none" {
			url = "http://" + hosts[0]
		}
	}
	lg.line(fmt.Sprintf("[islet] live in %s %s", time.Since(start).Round(time.Second), url))
	if s.bus != nil {
		s.bus.Emit(context.Background(), notify.Event{Category: "deploy", Severity: notify.Info, Title: "Deployed: " + a.Name, Message: fmt.Sprintf("Release #%d is live (%s). %s", rel.Number, time.Since(start).Round(time.Second), url), Link: "/apps?app=" + a.ID})
	}
}

func (s *Service) deployCompose(ctx context.Context, a *App, rel *Release, lg *logger, dir string, runEnv []string) error {
	lg.step("compose up")
	d := Detect(dir)
	compose, err := os.ReadFile(filepath.Join(dir, d.Compose))
	if err != nil {
		return err
	}
	stack := "app-" + a.Name
	if err := s.dk.WriteStack(ctx, "deploy", stack, string(compose), strings.Join(runEnv, "\n")+"\n"); err != nil {
		return err
	}
	rc, wait, err := s.dk.StackAction(ctx, "deploy", stack, "up")
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		lg.line(sc.Text())
	}
	if err := wait(); err != nil {
		return err
	}
	rel.Container = stack
	if a.Domain != "" && a.StartCmd != "" { // StartCmd carries the web service name for compose apps
		target := fmt.Sprintf("%s-%s-1", stack, a.StartCmd)
		lg.step("route " + a.Domain + " → " + target)
		if err := s.px.Connect(ctx, "deploy", target); err != nil {
			return err
		}
		if err := s.route(ctx, "deploy", a, target); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) route(ctx context.Context, actor string, a *App, container string) error {
	doms, err := s.px.Domains(ctx)
	if err != nil {
		return err
	}
	for _, host := range a.Domains() {
		var d *proxy.Domain
		for i := range doms {
			if doms[i].Host == host {
				d = &doms[i]
			}
		}
		if d == nil {
			d = &proxy.Domain{Host: host, TLS: a.TLS, Enabled: true}
		}
		d.TargetType, d.Target, d.Port = "container", container, a.Port
		if _, err := s.px.Save(ctx, actor, d); err != nil {
			return fmt.Errorf("%s: %w", host, err)
		}
	}
	return nil
}

func (s *Service) healthy(ctx context.Context, lg *logger, container string, port int, path string) error {
	deadline := time.Now().Add(90 * time.Second)
	url := fmt.Sprintf("http://%s:%d%s", container, port, path)
	var last string
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		out, err := s.run.Run(ctx, "deploy", "docker", "inspect", "--format", "{{.State.Status}}", container)
		if err != nil || strings.TrimSpace(out.Stdout) != "running" {
			return fmt.Errorf("container is %s", strings.TrimSpace(out.Stdout))
		}
		res, err := s.run.Run(ctx, "deploy", "docker", "run", "--rm", "--network", proxy.NetworkName, "alpine:3", "sh", "-c", "wget -q -T 5 -O /dev/null --server-response "+shellQuote(url)+" 2>&1 | grep -m1 HTTP/ || exit 1")
		if err == nil {
			status := strings.TrimSpace(res.Stdout)
			if strings.Contains(status, " 2") || strings.Contains(status, " 3") {
				return nil
			}
			last = status
		} else {
			last = "not answering yet"
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("health check failed after 90s (%s)", last)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (s *Service) docker(ctx context.Context, lg *logger, args ...string) error {
	rc, wait, err := s.run.Stream(ctx, "deploy", "docker", args...)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lg.line(sc.Text())
	}
	if err := wait(); err != nil {
		return fmt.Errorf("docker %s failed", args[0])
	}
	return nil
}

func (s *Service) pruneImages(ctx context.Context, a *App) {
	rels, err := s.Releases(ctx, a.ID, 50)
	if err != nil {
		return
	}
	keep := 5
	for _, r := range rels {
		if r.Image == "" || r.Status == "live" {
			continue
		}
		if keep > 0 && (r.Status == "superseded" || r.Status == "failed") {
			keep--
			continue
		}
		if a.Source != "image" {
			_, _ = s.run.Run(ctx, "deploy", "docker", "rmi", "-f", r.Image)
			_, _ = s.st.DB.ExecContext(ctx, `UPDATE releases SET image = '' WHERE id = ?`, r.ID)
		}
	}
}

// ---- helpers ----

func applyDetection(a *App, d Detection) {
	a.Strategy, a.Framework = d.Strategy, d.Framework
	if a.InstallCmd == "" {
		a.InstallCmd = d.InstallCmd
	}
	if a.BuildCmd == "" {
		a.BuildCmd = d.BuildCmd
	}
	if a.StartCmd == "" {
		a.StartCmd = d.StartCmd
	}
	if a.OutputDir == "" {
		a.OutputDir = d.OutputDir
	}
	if a.Port == 0 {
		a.Port = d.Port
	}
	if a.Strategy == "static" {
		a.Port = 80
	}
	if a.NodeVer == "" {
		a.NodeVer = d.NodeVer
	}
	if a.PyVer == "" {
		a.PyVer = d.PyVer
	}
}

func envLines(env string) []string {
	var out []string
	for _, l := range strings.Split(env, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// splitEnv separates build-time variables from runtime ones. Public
// front-end prefixes are baked in; everything else is injected at run.
func splitEnv(lines []string) (build, run []string) {
	for _, kv := range lines {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "ISLET_") {
			continue
		}
		switch {
		case strings.HasPrefix(k, "NEXT_PUBLIC_"), strings.HasPrefix(k, "VITE_"), strings.HasPrefix(k, "PUBLIC_"), strings.HasPrefix(k, "REACT_APP_"), strings.HasPrefix(k, "NUXT_PUBLIC_"), strings.HasPrefix(k, "ASTRO_"):
			build = append(build, kv)
			run = append(run, kv)
		default:
			run = append(run, kv)
		}
	}
	return
}

func gitHead(dir string) (sha, msg, author string) {
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%H%n%an%n%s").Output()
	if err != nil {
		return "", "", ""
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\n", 3)
	if len(parts) == 3 {
		return parts[0], parts[2], parts[1]
	}
	return "", "", ""
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// VerifyWebhook checks a push payload signature from GitHub, GitLab or Gitea.
func VerifyWebhook(secret string, body []byte, ghSig, glToken, giteaSig string) bool {
	if glToken != "" {
		return hmac.Equal([]byte(glToken), []byte(secret))
	}
	sig := ghSig
	if sig == "" {
		sig = giteaSig
	}
	sig = strings.TrimPrefix(sig, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(sig))
}

// PushBranch extracts the branch from a push payload ref (refs/heads/main).
func PushBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
