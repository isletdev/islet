// Package runner keeps self-hosted CI runners alive in Docker. GitHub
// runners are ephemeral: each container takes one job and exits, a
// reconciler keeps a few idle ones ready and webhook queue events scale up
// to the pool's maximum. GitLab and Gitea runners are long-lived.
package runner

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// Images used for each provider. Pinned majors, pulled on first use.
const (
	githubImage = "myoung34/github-runner:latest"
	gitlabImage = "gitlab/gitlab-runner:alpine"
	giteaImage  = "gitea/act_runner:latest"
)

// Pool is a group of runners for one repository, organisation or instance.
type Pool struct {
	ID            string  `json:"id"`
	Provider      string  `json:"provider"`
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	Token         string  `json:"token,omitempty"`
	Labels        string  `json:"labels"`
	MinIdle       int     `json:"minIdle"`
	MaxRunners    int     `json:"maxRunners"`
	DockerAccess  bool    `json:"dockerAccess"`
	MemoryMB      int     `json:"memoryMb"`
	CPUs          float64 `json:"cpus"`
	WebhookSecret string  `json:"webhookSecret,omitempty"`
	Enabled       bool    `json:"enabled"`
	CreatedAt     string  `json:"createdAt"`

	// Derived
	Runners []Runner `json:"runners"`
	Idle    int      `json:"idle"`
	Busy    int      `json:"busy"`
	Error   string   `json:"error,omitempty"`
}

// Runner is one container.
type Runner struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Busy    bool   `json:"busy"`
	Started string `json:"started"`
}

// Job is a workflow job seen through webhooks.
type Job struct {
	ID         int64  `json:"id"`
	PoolID     string `json:"poolId"`
	ExternalID string `json:"externalId"`
	Name       string `json:"name"`
	Repo       string `json:"repo"`
	Runner     string `json:"runner"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
	QueuedAt   string `json:"queuedAt"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// ErrNotFound is returned for unknown pools.
var ErrNotFound = errors.New("runner pool not found")

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Service manages pools.
type Service struct {
	st   *store.Store
	keys *auth.Keys
	run  *cmdrun.Runner
	bus  *notify.Bus
	log  *slog.Logger
	mu   sync.Mutex
	seq  int

	// RegToken may mint a GitHub runner registration token for a repo or org URL (GitHub App).
	RegToken func(ctx context.Context, scopeURL string) (string, error)
	// AppConfigured reports whether the GitHub App can stand in for a token.
	AppConfigured func(ctx context.Context) bool
	// scale serialises counting runners and starting one.
	scale sync.Mutex
}

// New builds the service.
func New(st *store.Store, keys *auth.Keys, run *cmdrun.Runner, bus *notify.Bus, log *slog.Logger) *Service {
	return &Service{st: st, keys: keys, run: run, bus: bus, log: log}
}

// Start runs the reconciler until ctx ends.
func (s *Service) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			s.reconcileAll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// Validate normalises a pool.
func (p *Pool) Validate() error {
	p.Name = strings.ToLower(strings.TrimSpace(p.Name))
	if !nameRe.MatchString(p.Name) {
		return errors.New("name must be 1-40 lowercase letters, digits or dashes")
	}
	p.URL = strings.TrimRight(strings.TrimSpace(p.URL), "/")
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return errors.New("URL must be an https:// address")
	}
	switch p.Provider {
	case "github":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if u.Host != "github.com" || len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
			return errors.New("GitHub URL must be https://github.com/org or https://github.com/org/repo")
		}
	case "gitlab", "gitea":
	default:
		return errors.New("provider must be github, gitlab or gitea")
	}
	if p.MinIdle < 0 || p.MinIdle > 10 {
		return errors.New("idle runners must be between 0 and 10")
	}
	if p.MaxRunners < 1 || p.MaxRunners > 20 {
		return errors.New("maximum runners must be between 1 and 20")
	}
	if p.MinIdle > p.MaxRunners {
		p.MinIdle = p.MaxRunners
	}
	if p.Provider != "github" {
		p.MinIdle, p.MaxRunners = 1, 1 // long-lived runners: one container per pool
	}
	p.Labels = strings.Join(strings.Fields(strings.ReplaceAll(p.Labels, ",", " ")), ",")
	return nil
}

// ---- CRUD ----

const cols = `id, provider, name, url, token, labels, min_idle, max_runners, docker_access, memory_mb, cpus, webhook_secret, enabled, created_at`

func (s *Service) scan(sc interface{ Scan(...any) error }) (Pool, error) {
	var p Pool
	var tok []byte
	err := sc.Scan(&p.ID, &p.Provider, &p.Name, &p.URL, &tok, &p.Labels, &p.MinIdle, &p.MaxRunners, &p.DockerAccess, &p.MemoryMB, &p.CPUs, &p.WebhookSecret, &p.Enabled, &p.CreatedAt)
	if err != nil {
		return p, err
	}
	if len(tok) > 0 {
		if b, err := s.keys.Decrypt(tok); err == nil {
			p.Token = string(b)
		}
	}
	return p, nil
}

// List returns pools with their live runners.
func (s *Service) List(ctx context.Context) ([]Pool, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM runner_pools WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pool{}
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		s.decorate(ctx, &p)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns one pool.
func (s *Service) Get(ctx context.Context, id string) (*Pool, error) {
	p, err := s.scan(s.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM runner_pools WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.decorate(ctx, &p)
	return &p, nil
}

func (s *Service) decorate(ctx context.Context, p *Pool) {
	p.Runners = s.runners(ctx, p)
	for _, r := range p.Runners {
		if r.State != "running" {
			continue
		}
		if r.Busy {
			p.Busy++
		} else {
			p.Idle++
		}
	}
}

// Save inserts or updates a pool and reconciles it.
func (s *Service) Save(ctx context.Context, p *Pool) (*Pool, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	tok := []byte{}
	if p.Token != "" {
		b, err := s.keys.Encrypt([]byte(p.Token))
		if err != nil {
			return nil, err
		}
		tok = b
	}
	if p.ID == "" {
		if p.Token == "" && !(p.Provider == "github" && s.AppConfigured != nil && s.AppConfigured(ctx)) {
			return nil, errors.New("a token is required: a GitHub personal access token (repo or admin:org), a GitLab runner authentication token, or a Gitea registration token")
		}
		p.ID = randHex(6)
		p.WebhookSecret = randHex(24)
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO runner_pools (id, server_id, provider, name, url, token, labels, min_idle, max_runners, docker_access, memory_mb, cpus, webhook_secret, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, s.st.ServerID, p.Provider, p.Name, p.URL, tok, p.Labels, p.MinIdle, p.MaxRunners, p.DockerAccess, p.MemoryMB, p.CPUs, p.WebhookSecret, p.Enabled)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("a pool with that name already exists")
			}
			return nil, err
		}
	} else {
		old, err := s.Get(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if p.Name != old.Name {
			return nil, errors.New("pools cannot be renamed")
		}
		if p.Token == "" {
			tok = nil // keep the stored token
		}
		q := `UPDATE runner_pools SET url = ?, labels = ?, min_idle = ?, max_runners = ?, docker_access = ?, memory_mb = ?, cpus = ?, enabled = ?`
		args := []any{p.URL, p.Labels, p.MinIdle, p.MaxRunners, p.DockerAccess, p.MemoryMB, p.CPUs, p.Enabled}
		if tok != nil {
			q += `, token = ?`
			args = append(args, tok)
		}
		args = append(args, p.ID)
		if _, err := s.st.DB.ExecContext(ctx, q+` WHERE id = ?`, args...); err != nil {
			return nil, err
		}
		// Settings that live inside the container need a fresh start.
		if old.URL != p.URL || old.Labels != p.Labels || old.DockerAccess != p.DockerAccess || tok != nil || !p.Enabled {
			s.stopAll(ctx, old)
		}
	}
	saved, err := s.Get(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	go s.reconcile(context.Background(), saved)
	return saved, nil
}

// Delete stops the runners and removes the pool.
func (s *Service) Delete(ctx context.Context, id string) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	s.stopAll(ctx, p)
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM runner_jobs WHERE pool_id = ?`, id)
	_, err = s.st.DB.ExecContext(ctx, `DELETE FROM runner_pools WHERE id = ?`, id)
	return err
}

// Jobs lists recent workflow jobs for a pool.
func (s *Service) Jobs(ctx context.Context, poolID string, limit int) ([]Job, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, pool_id, external_id, name, repo, runner, status, conclusion, url, queued_at, started_at, finished_at FROM runner_jobs WHERE pool_id = ? ORDER BY id DESC LIMIT ?`, poolID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.PoolID, &j.ExternalID, &j.Name, &j.Repo, &j.Runner, &j.Status, &j.Conclusion, &j.URL, &j.QueuedAt, &j.StartedAt, &j.FinishedAt); err == nil {
			out = append(out, j)
		}
	}
	return out, nil
}

// ---- containers ----

func (s *Service) prefix(p *Pool) string { return "islet-runner-" + p.Name + "-" }

func (s *Service) runners(ctx context.Context, p *Pool) []Runner {
	res, err := s.run.Run(ctx, "system", "docker", "ps", "-a", "--filter", "label=islet.runner="+p.ID, "--format", "{{.Names}}\t{{.State}}\t{{.CreatedAt}}\t{{.Label \"islet.runner.busy\"}}")
	if err != nil {
		return nil
	}
	out := []Runner{}
	for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		r := Runner{Name: f[0], State: f[1], Started: f[2]}
		// A GitHub ephemeral runner is busy once it has printed "Running job".
		if p.Provider == "github" && r.State == "running" {
			if logs, err := s.run.Run(ctx, "system", "docker", "logs", "--tail", "50", r.Name); err == nil && strings.Contains(logs.Stdout+logs.Stderr, "Running job") {
				r.Busy = true
			}
		}
		out = append(out, r)
	}
	return out
}

func (s *Service) stopAll(ctx context.Context, p *Pool) {
	for _, r := range s.runners(ctx, p) {
		_, _ = s.run.Run(ctx, "system", "docker", "rm", "-f", r.Name)
	}
}

func (s *Service) startOne(ctx context.Context, p *Pool) error {
	s.mu.Lock()
	s.seq++
	name := fmt.Sprintf("%s%d-%s", s.prefix(p), s.seq, randHex(2))
	s.mu.Unlock()
	args := []string{"run", "-d", "--name", name, "--label", "islet.runner=" + p.ID, "--log-opt", "max-size=10m", "--log-opt", "max-file=2"}
	if p.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", p.MemoryMB))
	}
	if p.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(p.CPUs, 'f', -1, 64))
	}
	if p.DockerAccess {
		args = append(args, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	}
	switch p.Provider {
	case "github":
		u, _ := url.Parse(p.URL)
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		args = append(args, "--rm", "-e", "EPHEMERAL=true", "-e", "DISABLE_AUTO_UPDATE=true", "-e", "RUNNER_NAME="+name, "-e", "RUNNER_WORKDIR=/tmp/work")
		if p.Token != "" {
			args = append(args, "-e", "ACCESS_TOKEN="+p.Token)
		} else if s.RegToken != nil {
			reg, err := s.RegToken(ctx, p.URL)
			if err != nil {
				return fmt.Errorf("GitHub App could not mint a registration token: %w", err)
			}
			args = append(args, "-e", "RUNNER_TOKEN="+reg)
		} else {
			return errors.New("no token and no GitHub App configured")
		}
		if p.Labels != "" {
			args = append(args, "-e", "LABELS="+p.Labels)
		}
		if len(parts) == 2 {
			args = append(args, "-e", "RUNNER_SCOPE=repo", "-e", "REPO_URL="+p.URL)
		} else {
			args = append(args, "-e", "RUNNER_SCOPE=org", "-e", "ORG_NAME="+parts[0])
		}
		args = append(args, "-v", "islet-runner-cache-"+p.Name+":/tmp/work", githubImage)
	case "gitlab":
		// A runner authentication token (glrt-…) from the GitLab UI; the container registers itself.
		args = append(args, "--restart", "unless-stopped", "-v", "islet-runner-config-"+p.Name+":/etc/gitlab-runner", gitlabImage)
		// Registration is done once by an exec after start (see below).
	case "gitea":
		args = append(args, "--restart", "unless-stopped", "-e", "GITEA_INSTANCE_URL="+p.URL, "-e", "GITEA_RUNNER_REGISTRATION_TOKEN="+p.Token, "-e", "GITEA_RUNNER_NAME="+name)
		if p.Labels != "" {
			args = append(args, "-e", "GITEA_RUNNER_LABELS="+p.Labels)
		}
		args = append(args, "-v", "islet-runner-data-"+p.Name+":/data", giteaImage)
	}
	if _, err := s.run.Run(ctx, "system", "docker", args...); err != nil {
		var ce *cmdrun.Error
		if errors.As(err, &ce) {
			return errors.New(strings.TrimSpace(ce.Result.Stderr))
		}
		return err
	}
	if p.Provider == "gitlab" {
		reg := []string{"exec", name, "gitlab-runner", "register", "--non-interactive", "--url", p.URL, "--token", p.Token, "--executor", "docker", "--docker-image", "alpine:3", "--name", name}
		if p.DockerAccess {
			reg = append(reg, "--docker-volumes", "/var/run/docker.sock:/var/run/docker.sock")
		}
		if _, err := s.run.Run(ctx, "system", "docker", reg...); err != nil {
			_, _ = s.run.Run(ctx, "system", "docker", "rm", "-f", name)
			return fmt.Errorf("gitlab-runner register failed: %w", err)
		}
	}
	return nil
}

// reconcile keeps min_idle idle ephemeral runners, within max.
func (s *Service) reconcile(ctx context.Context, p *Pool) {
	if !p.Enabled {
		s.stopAll(ctx, p)
		return
	}
	runners := s.runners(ctx, p)
	// Sweep exited containers (non --rm providers) so counts stay honest.
	running, idle := 0, 0
	for _, r := range runners {
		if r.State == "running" {
			running++
			if !r.Busy {
				idle++
			}
		} else {
			_, _ = s.run.Run(ctx, "system", "docker", "rm", "-f", r.Name)
		}
	}
	want := p.MinIdle - idle
	for i := 0; i < want && running < p.MaxRunners; i++ {
		if err := s.startOne(ctx, p); err != nil {
			s.log.Warn("runner start failed", "pool", p.Name, "err", err)
			_, _ = s.st.DB.ExecContext(ctx, `UPDATE runner_pools SET enabled = enabled WHERE id = ?`, p.ID)
			if s.bus != nil {
				s.bus.Emit(ctx, notify.Event{Category: "runner", Severity: notify.Warning, Title: "Runner start failed: " + p.Name, Message: err.Error(), Link: "/runners"})
			}
			return
		}
		running++
	}
}

func (s *Service) reconcileAll(ctx context.Context) {
	pools, err := s.List(ctx)
	if err != nil {
		return
	}
	for i := range pools {
		s.reconcile(ctx, &pools[i])
	}
}

// ScaleUp starts one more runner for a queued job when under the maximum.
//
// Counting and starting happen under one lock per pool. A matrix build makes
// GitHub deliver many queued webhooks at once, and each one runs this; without
// the lock every caller sees the same count and they all start a runner, so a
// pool capped at two ends up with ten and the server runs out of memory.
func (s *Service) ScaleUp(ctx context.Context, p *Pool) {
	if !p.Enabled || p.Provider != "github" {
		return
	}
	s.scale.Lock()
	defer s.scale.Unlock()
	running := 0
	for _, r := range s.runners(ctx, p) {
		if r.State == "running" {
			running++
		}
	}
	if running >= p.MaxRunners {
		return
	}
	if err := s.startOne(ctx, p); err != nil {
		s.log.Warn("runner scale-up failed", "pool", p.Name, "err", err)
	}
}

// ---- webhooks ----

// VerifyGitHub checks the X-Hub-Signature-256 header.
func VerifyGitHub(secret string, body []byte, sig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(strings.TrimPrefix(sig, "sha256=")))
}

// HandleWorkflowJob records a workflow_job event and scales when queued.
func (s *Service) HandleWorkflowJob(ctx context.Context, p *Pool, body []byte) (string, error) {
	var ev struct {
		Action      string `json:"action"`
		WorkflowJob struct {
			ID          int64    `json:"id"`
			Name        string   `json:"name"`
			HTMLURL     string   `json:"html_url"`
			Status      string   `json:"status"`
			Conclusion  string   `json:"conclusion"`
			Labels      []string `json:"labels"`
			RunnerName  string   `json:"runner_name"`
			StartedAt   string   `json:"started_at"`
			CompletedAt string   `json:"completed_at"`
		} `json:"workflow_job"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", err
	}
	j := ev.WorkflowJob
	selfHosted := false
	for _, l := range j.Labels {
		if l == "self-hosted" {
			selfHosted = true
		}
	}
	if !selfHosted {
		return "ignored: not self-hosted", nil
	}
	ext := strconv.FormatInt(j.ID, 10)
	switch ev.Action {
	case "queued":
		_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO runner_jobs (pool_id, external_id, name, repo, status, url) VALUES (?, ?, ?, ?, 'queued', ?)`, p.ID, ext, j.Name, ev.Repository.FullName, j.HTMLURL)
		go s.ScaleUp(context.Background(), p)
		return "runner starting", nil
	case "in_progress":
		_, _ = s.st.DB.ExecContext(ctx, `UPDATE runner_jobs SET status = 'in_progress', runner = ?, started_at = ? WHERE pool_id = ? AND external_id = ?`, j.RunnerName, j.StartedAt, p.ID, ext)
	case "completed":
		res, _ := s.st.DB.ExecContext(ctx, `UPDATE runner_jobs SET status = 'completed', conclusion = ?, runner = ?, finished_at = ? WHERE pool_id = ? AND external_id = ?`, j.Conclusion, j.RunnerName, j.CompletedAt, p.ID, ext)
		if n, _ := res.RowsAffected(); n == 0 {
			_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO runner_jobs (pool_id, external_id, name, repo, runner, status, conclusion, url, finished_at) VALUES (?, ?, ?, ?, ?, 'completed', ?, ?, ?)`, p.ID, ext, j.Name, ev.Repository.FullName, j.RunnerName, j.Conclusion, j.HTMLURL, j.CompletedAt)
		}
		if j.Conclusion == "failure" && s.bus != nil {
			s.bus.Emit(ctx, notify.Event{Category: "runner", Severity: notify.Warning, Title: "CI job failed: " + j.Name, Message: ev.Repository.FullName + " · " + j.HTMLURL, Link: "/runners"})
		}
		go s.reconcile(context.Background(), p)
	}
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM runner_jobs WHERE pool_id = ? AND id NOT IN (SELECT id FROM runner_jobs WHERE pool_id = ? ORDER BY id DESC LIMIT 200)`, p.ID, p.ID)
	return "recorded " + ev.Action, nil
}

// DeployWorkflow renders a GitHub Actions workflow that deploys an app
// through the daemon from a self-hosted runner on the same server.
func DeployWorkflow(appName, branch, labels string) string {
	runsOn := "[self-hosted"
	for _, l := range strings.Split(labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			runsOn += ", " + l
		}
	}
	runsOn += "]"
	return fmt.Sprintf(`name: Deploy %s
on:
  push:
    branches: [%s]
  workflow_dispatch:
concurrency: deploy-%s
jobs:
  deploy:
    runs-on: %s
    steps:
      - name: Deploy through Islet
        env:
          ISLET_URL: ${{ secrets.ISLET_URL }}       # https://your-server:9443, or https://host.docker.internal:9443 from a runner on the box
          ISLET_TOKEN: ${{ secrets.ISLET_TOKEN }}   # Settings → API tokens, scope "deploy"
        run: |
          set -euo pipefail
          id=$(curl -fsS -k -H "Authorization: Bearer $ISLET_TOKEN" "$ISLET_URL/api/v1/apps" | jq -r '.[] | select(.name=="%s") | .id')
          test -n "$id" || { echo "app %s not found"; exit 1; }
          curl -fsS -k -N -X POST -H "Authorization: Bearer $ISLET_TOKEN" -H "Content-Type: application/json" \
            "$ISLET_URL/api/v1/apps/$id/deploy" | sed -n 's/^data: //p' | tr -d '"' | tee /tmp/deploy.log
          ! grep -q '^error' /tmp/deploy.log
`, appName, branch, appName, runsOn, appName, appName)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
