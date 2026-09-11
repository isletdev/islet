// Package cron is the task runner: scheduled and on-demand jobs with live
// output, run history, retries, overlap policies, script versioning and
// heartbeat monitors for jobs that run elsewhere.
package cron

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	robfig "github.com/robfig/cron/v3"

	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// Job types.
const (
	TypeCommand   = "command"   // shell command line
	TypeScript    = "script"    // inline script stored under <data>/scripts
	TypeFile      = "file"      // existing executable on disk
	TypeContainer = "container" // docker exec in a running container
	TypeImage     = "image"     // one-off docker run --rm
	TypeHTTP      = "http"      // HTTP request, fails on 4xx/5xx
	TypeChain     = "chain"     // other jobs, in order, stop on first failure
	TypeHeartbeat = "heartbeat" // expects a ping; alerts when it stops coming
)

var validTypes = map[string]bool{TypeCommand: true, TypeScript: true, TypeFile: true, TypeContainer: true, TypeImage: true, TypeHTTP: true, TypeChain: true, TypeHeartbeat: true}

// Job is one scheduled task.
type Job struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Schedule   string `json:"schedule"`
	Timezone   string `json:"timezone"`
	Command    string `json:"command"`
	Script     string `json:"script"`
	Container  string `json:"container"`
	HTTPMethod string `json:"httpMethod"`
	WorkDir    string `json:"workDir"`
	RunAs      string `json:"runAs"`
	TimeoutSec int    `json:"timeoutSec"`
	Overlap    string `json:"overlap"`
	Retries    int    `json:"retries"`
	Nice       int    `json:"nice"`
	JitterSec  int    `json:"jitterSec"`
	GraceSec   int    `json:"graceSec"`
	NotifyOn   string `json:"notifyOn"`
	Enabled    bool   `json:"enabled"`
	LastPingAt string `json:"lastPingAt"`
	Overdue    bool   `json:"overdue"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`

	// Derived, filled by List/Get.
	NextRun   string `json:"nextRun"`
	LastRun   *Run   `json:"lastRun,omitempty"`
	Running   bool   `json:"running"`
	Described string `json:"described"`
}

// Run is one execution.
type Run struct {
	ID         int64  `json:"id"`
	JobID      string `json:"jobId"`
	Trigger    string `json:"trigger"`
	Attempt    int    `json:"attempt"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output,omitempty"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	DurationMs int64  `json:"durationMs"`
}

// ScriptVersion is a saved copy of a script body.
type ScriptVersion struct {
	ID        int64  `json:"id"`
	Actor     string `json:"actor"`
	Content   string `json:"content,omitempty"`
	CreatedAt string `json:"createdAt"`
}

const sqlTime = "2006-01-02T15:04:05.000Z"
const maxOutput = 256 << 10

var parser = robfig.NewParser(robfig.Minute | robfig.Hour | robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,79}$`)

// Service owns the scheduler.
type Service struct {
	st        *store.Store
	bus       *notify.Bus
	log       *slog.Logger
	scriptDir string
	cron      *robfig.Cron

	mu      sync.Mutex
	entries map[string]robfig.EntryID
	active  map[string]*activeRun
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds the service; call Start to load jobs and begin scheduling.
func New(st *store.Store, bus *notify.Bus, dataDir string, log *slog.Logger) *Service {
	abs, _ := filepath.Abs(dataDir)
	return &Service{st: st, bus: bus, log: log, scriptDir: filepath.Join(abs, "scripts"), cron: robfig.New(robfig.WithParser(parser)), entries: map[string]robfig.EntryID{}, active: map[string]*activeRun{}}
}

// Start loads enabled jobs, starts the scheduler and the heartbeat watcher.
func (s *Service) Start(ctx context.Context) error {
	if err := os.MkdirAll(s.scriptDir, 0o755); err != nil {
		return err
	}
	// Runs left "running" by a previous daemon are marked killed.
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE job_runs SET status = 'killed', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE status = 'running'`)
	jobs, err := s.List(ctx)
	if err != nil {
		return err
	}
	for i := range jobs {
		s.schedule(&jobs[i])
	}
	s.cron.Start()
	go s.watchHeartbeats(ctx)
	go func() {
		<-ctx.Done()
		<-s.cron.Stop().Done()
	}()
	return nil
}

func (s *Service) spec(j *Job) string {
	if j.Timezone != "" {
		return "CRON_TZ=" + j.Timezone + " " + j.Schedule
	}
	return j.Schedule
}

func (s *Service) schedule(j *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[j.ID]; ok {
		s.cron.Remove(id)
		delete(s.entries, j.ID)
	}
	if !j.Enabled || j.Schedule == "" || j.Type == TypeHeartbeat {
		return
	}
	jobID := j.ID
	id, err := s.cron.AddFunc(s.spec(j), func() {
		if j.JitterSec > 0 {
			time.Sleep(time.Duration(randInt(j.JitterSec)) * time.Second)
		}
		_ = s.Run(context.Background(), jobID, "schedule", nil)
	})
	if err != nil {
		s.log.Warn("cron: cannot schedule", "job", j.Name, "err", err)
		return
	}
	s.entries[j.ID] = id
}

// Preview parses a schedule and returns its description and next runs.
func Preview(schedule, tz string, n int) (string, []time.Time, error) {
	if strings.TrimSpace(schedule) == "" {
		return Describe(""), nil, nil
	}
	loc := time.Local
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return "", nil, fmt.Errorf("unknown timezone %q", tz)
		}
		loc = l
	}
	sched, err := parser.Parse(schedule)
	if err != nil {
		return "", nil, fmt.Errorf("invalid schedule: %w", err)
	}
	t := time.Now().In(loc)
	var out []time.Time
	for i := 0; i < n; i++ {
		t = sched.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return Describe(schedule), out, nil
}

// Validate checks a job before saving.
func (j *Job) Validate() error {
	j.Name = strings.TrimSpace(j.Name)
	if !nameRe.MatchString(j.Name) {
		return errors.New("name must be 1-80 letters, digits, spaces, dots, dashes or underscores")
	}
	if !validTypes[j.Type] {
		return errors.New("unknown job type")
	}
	j.Schedule = strings.TrimSpace(j.Schedule)
	if j.Schedule != "" || j.Type == TypeHeartbeat {
		if j.Type == TypeHeartbeat && j.Schedule == "" {
			return errors.New("a heartbeat needs a schedule to know when a ping is late")
		}
		if _, _, err := Preview(j.Schedule, j.Timezone, 1); err != nil {
			return err
		}
	}
	switch j.Type {
	case TypeCommand:
		if strings.TrimSpace(j.Command) == "" {
			return errors.New("command is required")
		}
	case TypeScript:
		if strings.TrimSpace(j.Script) == "" {
			return errors.New("script body is required")
		}
	case TypeFile:
		if !filepath.IsAbs(j.Command) {
			return errors.New("file must be an absolute path")
		}
	case TypeContainer:
		if j.Container == "" || strings.TrimSpace(j.Script) == "" {
			return errors.New("container and command are required")
		}
	case TypeImage:
		if strings.TrimSpace(j.Command) == "" {
			return errors.New("image is required")
		}
	case TypeHTTP:
		if !strings.HasPrefix(j.Command, "http://") && !strings.HasPrefix(j.Command, "https://") {
			return errors.New("URL must start with http:// or https://")
		}
		if j.HTTPMethod == "" {
			j.HTTPMethod = "GET"
		}
	case TypeChain:
		if strings.TrimSpace(j.Command) == "" {
			return errors.New("pick at least one job for the chain")
		}
	}
	switch j.Overlap {
	case "", "skip":
		j.Overlap = "skip"
	case "queue", "kill":
	default:
		return errors.New("overlap must be skip, queue or kill")
	}
	switch j.NotifyOn {
	case "", "failure":
		j.NotifyOn = "failure"
	case "always", "never":
	default:
		return errors.New("notifyOn must be failure, always or never")
	}
	if j.TimeoutSec <= 0 {
		j.TimeoutSec = 3600
	}
	if j.Retries < 0 || j.Retries > 10 {
		return errors.New("retries must be between 0 and 10")
	}
	if j.Nice < -20 || j.Nice > 19 {
		return errors.New("nice must be between -20 and 19")
	}
	if j.RunAs != "" && !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(j.RunAs) {
		return errors.New("run as must be a unix user name")
	}
	return nil
}

// ---- CRUD ----

const jobCols = `id, name, type, schedule, timezone, command, script, container, http_method, work_dir, run_as, timeout_sec, overlap, retries, nice, jitter_sec, grace_sec, notify_on, enabled, last_ping_at, overdue, created_at, updated_at`

func scanJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := sc.Scan(&j.ID, &j.Name, &j.Type, &j.Schedule, &j.Timezone, &j.Command, &j.Script, &j.Container, &j.HTTPMethod, &j.WorkDir, &j.RunAs, &j.TimeoutSec, &j.Overlap, &j.Retries, &j.Nice, &j.JitterSec, &j.GraceSec, &j.NotifyOn, &j.Enabled, &j.LastPingAt, &j.Overdue, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// List returns every job with derived state.
func (s *Service) List(ctx context.Context) ([]Job, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		s.decorate(ctx, &j)
		out = append(out, j)
	}
	return out, rows.Err()
}

// Get returns one job.
func (s *Service) Get(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(s.st.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.decorate(ctx, &j)
	return &j, nil
}

// ErrNotFound is returned for unknown jobs.
var ErrNotFound = errors.New("job not found")

func (s *Service) decorate(ctx context.Context, j *Job) {
	j.Described = Describe(j.Schedule)
	if j.Enabled && j.Schedule != "" {
		if _, next, err := Preview(j.Schedule, j.Timezone, 1); err == nil && len(next) == 1 {
			j.NextRun = next[0].UTC().Format(sqlTime)
		}
	}
	if r, err := s.lastRun(ctx, j.ID); err == nil {
		j.LastRun = r
	}
	s.mu.Lock()
	_, j.Running = s.active[j.ID]
	s.mu.Unlock()
}

func (s *Service) lastRun(ctx context.Context, jobID string) (*Run, error) {
	var r Run
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, job_id, trigger, attempt, status, exit_code, started_at, finished_at, duration_ms FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT 1`, jobID).
		Scan(&r.ID, &r.JobID, &r.Trigger, &r.Attempt, &r.Status, &r.ExitCode, &r.StartedAt, &r.FinishedAt, &r.DurationMs)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Save inserts or updates a job and reschedules it.
func (s *Service) Save(ctx context.Context, actor string, j *Job) (*Job, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	isNew := j.ID == ""
	if isNew {
		j.ID = randID()
		if j.Type == TypeHeartbeat {
			j.Command = randID() + randID()
		}
	}
	var prevScript string
	if !isNew {
		old, err := s.Get(ctx, j.ID)
		if err != nil {
			return nil, err
		}
		prevScript = old.Script
		if j.Type == TypeHeartbeat {
			j.Command = old.Command // ping token never changes
		}
	}
	if j.Type != TypeScript && j.Type != TypeContainer && j.Type != TypeImage {
		j.Script = ""
	}
	if isNew {
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO jobs (id, server_id, name, type, schedule, timezone, command, script, container, http_method, work_dir, run_as, timeout_sec, overlap, retries, nice, jitter_sec, grace_sec, notify_on, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.ID, s.st.ServerID, j.Name, j.Type, j.Schedule, j.Timezone, j.Command, j.Script, j.Container, j.HTTPMethod, j.WorkDir, j.RunAs, j.TimeoutSec, j.Overlap, j.Retries, j.Nice, j.JitterSec, j.GraceSec, j.NotifyOn, j.Enabled)
		if err != nil {
			return nil, err
		}
	} else {
		_, err := s.st.DB.ExecContext(ctx, `UPDATE jobs SET name = ?, type = ?, schedule = ?, timezone = ?, command = ?, script = ?, container = ?, http_method = ?, work_dir = ?, run_as = ?, timeout_sec = ?, overlap = ?, retries = ?, nice = ?, jitter_sec = ?, grace_sec = ?, notify_on = ?, enabled = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
			j.Name, j.Type, j.Schedule, j.Timezone, j.Command, j.Script, j.Container, j.HTTPMethod, j.WorkDir, j.RunAs, j.TimeoutSec, j.Overlap, j.Retries, j.Nice, j.JitterSec, j.GraceSec, j.NotifyOn, j.Enabled, j.ID)
		if err != nil {
			return nil, err
		}
	}
	if j.Type == TypeScript {
		if err := s.writeScript(j); err != nil {
			return nil, err
		}
		if j.Script != prevScript {
			_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO script_versions (job_id, actor, content) VALUES (?, ?, ?)`, j.ID, actor, j.Script)
			_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM script_versions WHERE job_id = ? AND id NOT IN (SELECT id FROM script_versions WHERE job_id = ? ORDER BY id DESC LIMIT 30)`, j.ID, j.ID)
		}
	} else {
		_ = os.Remove(s.scriptPath(j.ID))
	}
	s.schedule(j)
	return s.Get(ctx, j.ID)
}

// Delete removes a job, its runs, versions and script file.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.Kill(id)
	s.mu.Lock()
	if e, ok := s.entries[id]; ok {
		s.cron.Remove(e)
		delete(s.entries, id)
	}
	s.mu.Unlock()
	_ = os.Remove(s.scriptPath(id))
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM job_runs WHERE job_id = ?`, id)
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM script_versions WHERE job_id = ?`, id)
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id = ? AND server_id = ?`, id, s.st.ServerID)
	return err
}

func (s *Service) scriptPath(id string) string { return filepath.Join(s.scriptDir, id+".sh") }

func (s *Service) writeScript(j *Job) error {
	body := strings.ReplaceAll(j.Script, "\r\n", "\n")
	if !strings.HasPrefix(body, "#!") {
		body = "#!/usr/bin/env bash\n" + body
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	p := s.scriptPath(j.ID)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		return err
	}
	return os.Chmod(p, 0o755)
}

// ScriptPath is where a script job lives on disk (for the UI and export).
func (s *Service) ScriptPath(id string) string { return s.scriptPath(id) }

// Versions lists saved script bodies, newest first.
func (s *Service) Versions(ctx context.Context, jobID string) ([]ScriptVersion, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, actor, created_at FROM script_versions WHERE job_id = ? ORDER BY id DESC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScriptVersion{}
	for rows.Next() {
		var v ScriptVersion
		if err := rows.Scan(&v.ID, &v.Actor, &v.CreatedAt); err == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// Version returns one saved body.
func (s *Service) Version(ctx context.Context, jobID string, id int64) (*ScriptVersion, error) {
	var v ScriptVersion
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, actor, content, created_at FROM script_versions WHERE job_id = ? AND id = ?`, jobID, id).Scan(&v.ID, &v.Actor, &v.Content, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, err
}

// Runs lists recent runs without output.
func (s *Service) Runs(ctx context.Context, jobID string, limit int) ([]Run, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, job_id, trigger, attempt, status, exit_code, started_at, finished_at, duration_ms FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.JobID, &r.Trigger, &r.Attempt, &r.Status, &r.ExitCode, &r.StartedAt, &r.FinishedAt, &r.DurationMs); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// RunDetail returns one run with output.
func (s *Service) RunDetail(ctx context.Context, jobID string, id int64) (*Run, error) {
	var r Run
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, job_id, trigger, attempt, status, exit_code, output, started_at, finished_at, duration_ms FROM job_runs WHERE job_id = ? AND id = ?`, jobID, id).
		Scan(&r.ID, &r.JobID, &r.Trigger, &r.Attempt, &r.Status, &r.ExitCode, &r.Output, &r.StartedAt, &r.FinishedAt, &r.DurationMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// Purge drops runs older than keep, keeping at least the last 20 per job.
func (s *Service) Purge(ctx context.Context, keep time.Duration) {
	cut := time.Now().Add(-keep).UTC().Format(sqlTime)
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM job_runs WHERE started_at < ? AND id NOT IN (SELECT id FROM job_runs r2 WHERE r2.job_id = job_runs.job_id ORDER BY id DESC LIMIT 20)`, cut)
}

// ---- execution ----

// Kill stops the active run of a job, if any.
func (s *Service) Kill(id string) bool {
	s.mu.Lock()
	a, ok := s.active[id]
	s.mu.Unlock()
	if !ok {
		return false
	}
	a.cancel()
	<-a.done
	return true
}

// Run executes a job now. Output goes to w (may be nil) as it is produced
// and is stored with the run. Retries happen inline. Returns the final error.
func (s *Service) Run(ctx context.Context, id, trigger string, w io.Writer) error {
	j, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if j.Type == TypeHeartbeat {
		return errors.New("heartbeats are pinged by the external job, not run here")
	}
	// Overlap policy.
	s.mu.Lock()
	if prev, running := s.active[id]; running {
		switch j.Overlap {
		case "skip":
			s.mu.Unlock()
			s.recordSkipped(ctx, j, trigger)
			return errors.New("previous run still in progress; skipped")
		case "kill":
			s.mu.Unlock()
			prev.cancel()
			<-prev.done
			s.mu.Lock()
		case "queue":
			s.mu.Unlock()
			<-prev.done
			s.mu.Lock()
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	a := &activeRun{cancel: cancel, done: make(chan struct{})}
	s.active[id] = a
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if s.active[id] == a {
			delete(s.active, id)
		}
		s.mu.Unlock()
		close(a.done)
	}()

	attempts := j.Retries + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		trig := trigger
		if attempt > 1 {
			trig = "retry"
			select {
			case <-runCtx.Done():
				return runCtx.Err()
			case <-time.After(30 * time.Second):
			}
		}
		err = s.runOnce(runCtx, j, trig, attempt, w)
		if err == nil {
			s.afterRun(ctx, j, true, "")
			return nil
		}
		if runCtx.Err() != nil {
			break
		}
	}
	s.afterRun(ctx, j, false, err.Error())
	return err
}

func (s *Service) recordSkipped(ctx context.Context, j *Job, trigger string) {
	_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO job_runs (job_id, trigger, status, output, finished_at) VALUES (?, ?, 'skipped', 'Skipped: the previous run was still in progress.', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, j.ID, trigger)
}

func (s *Service) afterRun(ctx context.Context, j *Job, ok bool, msg string) {
	if s.bus == nil || j.NotifyOn == "never" {
		return
	}
	link := "/cron?job=" + j.ID
	if ok {
		if j.NotifyOn == "always" {
			s.bus.Emit(ctx, notify.Event{Category: "cron", Subject: j.Name, Severity: notify.Info, Title: "Job finished: " + j.Name, Message: "Completed successfully.", Link: link})
		} else if prevFailed, _ := s.previousFailed(ctx, j.ID); prevFailed {
			s.bus.Emit(ctx, notify.Event{Category: "cron", Subject: j.Name, Severity: notify.Info, Title: "Job recovered: " + j.Name, Message: "The last run succeeded after an earlier failure.", Link: link})
		}
		return
	}
	s.bus.Emit(ctx, notify.Event{Category: "cron", Subject: j.Name, Severity: notify.Warning, Title: "Job failed: " + j.Name, Message: msg, Link: link})
}

func (s *Service) previousFailed(ctx context.Context, jobID string) (bool, error) {
	var st string
	err := s.st.DB.QueryRowContext(ctx, `SELECT status FROM job_runs WHERE job_id = ? AND status <> 'skipped' ORDER BY id DESC LIMIT 1 OFFSET 1`, jobID).Scan(&st)
	if err != nil {
		return false, err
	}
	return st == "failed" || st == "timeout", nil
}

// capture keeps the head and tail of the output and mirrors it to w.
type capture struct {
	mu   sync.Mutex
	head []byte
	tail []byte
	w    io.Writer
	over bool
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.head) < maxOutput/2 {
		n := min(len(p), maxOutput/2-len(c.head))
		c.head = append(c.head, p[:n]...)
		if n < len(p) {
			c.tail = append(c.tail, p[n:]...)
		}
	} else {
		c.over = true
		c.tail = append(c.tail, p...)
		if len(c.tail) > maxOutput/2 {
			c.tail = c.tail[len(c.tail)-maxOutput/2:]
		}
	}
	c.mu.Unlock()
	if c.w != nil {
		_, _ = c.w.Write(p)
	}
	return len(p), nil
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.over {
		return string(c.head) + "\n… output truncated …\n" + string(c.tail)
	}
	return string(c.head) + string(c.tail)
}

func (s *Service) runOnce(ctx context.Context, j *Job, trigger string, attempt int, w io.Writer) error {
	res, err := s.st.DB.ExecContext(ctx, `INSERT INTO job_runs (job_id, trigger, attempt, status) VALUES (?, ?, ?, 'running')`, j.ID, trigger, attempt)
	if err != nil {
		return err
	}
	runID, _ := res.LastInsertId()
	out := &capture{w: w}
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, time.Duration(j.TimeoutSec)*time.Second)
	defer cancel()

	var code int
	var runErr error
	switch j.Type {
	case TypeHTTP:
		code, runErr = s.runHTTP(tctx, j, out)
	case TypeChain:
		code, runErr = s.runChain(tctx, j, out)
	default:
		code, runErr = s.runProcess(tctx, j, out)
	}
	status := "success"
	switch {
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		status = "timeout"
		runErr = fmt.Errorf("timed out after %ds", j.TimeoutSec)
	case ctx.Err() != nil:
		status = "killed"
		runErr = errors.New("stopped")
	case runErr != nil:
		status = "failed"
	}
	dur := time.Since(start)
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
		fmt.Fprintf(out, "\n[islet] %s\n", msg)
	}
	_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE job_runs SET status = ?, exit_code = ?, output = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), duration_ms = ? WHERE id = ?`,
		status, code, out.String(), dur.Milliseconds(), runID)
	if runErr != nil {
		return fmt.Errorf("%s (%s)", msg, lastLine(out.String()))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" && !strings.HasPrefix(l, "[islet]") {
			if len(l) > 200 {
				l = l[:200] + "…"
			}
			return l
		}
	}
	return "no output"
}

func (s *Service) argv(j *Job) ([]string, error) {
	shell, shellFlag := "/bin/sh", "-c"
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("bash"); err == nil {
			shell = p
		} else {
			shell, shellFlag = "cmd", "/C"
		}
	}
	var argv []string
	switch j.Type {
	case TypeCommand:
		argv = []string{shell, shellFlag, j.Command}
	case TypeScript:
		if runtime.GOOS == "windows" {
			argv = []string{shell, s.scriptPath(j.ID)}
		} else {
			argv = []string{s.scriptPath(j.ID)}
		}
	case TypeFile:
		argv = []string{j.Command}
	case TypeContainer:
		argv = []string{"docker", "exec", j.Container, "sh", "-c", j.Script}
	case TypeImage:
		argv = []string{"docker", "run", "--rm", "--pull", "missing"}
		argv = append(argv, strings.Fields(j.Command)...)
		if strings.TrimSpace(j.Script) != "" {
			argv = append(argv, "sh", "-c", j.Script)
		}
	default:
		return nil, errors.New("unsupported type")
	}
	if runtime.GOOS == "linux" && (j.Type == TypeCommand || j.Type == TypeScript || j.Type == TypeFile) {
		if j.Nice != 0 {
			argv = append([]string{"nice", "-n", strconv.Itoa(j.Nice)}, argv...)
		}
		if j.RunAs != "" {
			argv = append([]string{"runuser", "-u", j.RunAs, "--"}, argv...)
		}
	}
	return argv, nil
}

func (s *Service) runProcess(ctx context.Context, j *Job, out io.Writer) (int, error) {
	argv, err := s.argv(j)
	if err != nil {
		return -1, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Dir = j.WorkDir
	cmd.Env = append(os.Environ(), "ISLET_JOB="+j.Name, "ISLET_JOB_ID="+j.ID)
	setProcAttrs(cmd)
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), fmt.Errorf("exit code %d", ee.ExitCode())
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (s *Service) runHTTP(ctx context.Context, j *Job, out io.Writer) (int, error) {
	req, err := http.NewRequestWithContext(ctx, j.HTTPMethod, j.Command, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("User-Agent", "islet-cron")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, err
	}
	defer res.Body.Close()
	fmt.Fprintf(out, "%s %s → %s\n", j.HTTPMethod, j.Command, res.Status)
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	out.Write(body)
	if res.StatusCode >= 400 {
		return res.StatusCode, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res.StatusCode, nil
}

func (s *Service) runChain(ctx context.Context, j *Job, out io.Writer) (int, error) {
	for _, id := range strings.Split(j.Command, ",") {
		id = strings.TrimSpace(id)
		if id == "" || id == j.ID {
			continue
		}
		child, err := s.Get(ctx, id)
		if err != nil {
			return -1, fmt.Errorf("chain step %s: %w", id, err)
		}
		fmt.Fprintf(out, "[islet] ── %s ──\n", child.Name)
		if err := s.Run(ctx, id, "chain", out); err != nil {
			return 1, fmt.Errorf("step %q failed: %w", child.Name, err)
		}
	}
	return 0, nil
}

// ---- heartbeats ----

// Ping records a heartbeat by token. Returns false when no job matches.
func (s *Service) Ping(ctx context.Context, token string) bool {
	var id, name string
	var overdue bool
	if err := s.st.DB.QueryRowContext(ctx, `SELECT id, name, overdue FROM jobs WHERE type = 'heartbeat' AND command = ? AND enabled = 1`, token).Scan(&id, &name, &overdue); err != nil {
		return false
	}
	now := time.Now().UTC().Format(sqlTime)
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE jobs SET last_ping_at = ?, overdue = 0 WHERE id = ?`, now, id)
	_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO job_runs (job_id, trigger, status, finished_at) VALUES (?, 'ping', 'success', ?)`, id, now)
	if overdue && s.bus != nil {
		s.bus.Emit(ctx, notify.Event{Category: "cron", Subject: name, Severity: notify.Info, Title: "Heartbeat missed: " + name, Message: "Recovered: the ping arrived again.", Link: "/cron?job=" + id})
	}
	return true
}

func (s *Service) watchHeartbeats(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := s.st.DB.QueryContext(ctx, `SELECT id, name, schedule, timezone, grace_sec, last_ping_at, created_at FROM jobs WHERE type = 'heartbeat' AND enabled = 1 AND overdue = 0 AND server_id = ?`, s.st.ServerID)
		if err != nil {
			continue
		}
		type hb struct {
			id, name, schedule, tz, last, created string
			grace                                 int
		}
		var list []hb
		for rows.Next() {
			var h hb
			if rows.Scan(&h.id, &h.name, &h.schedule, &h.tz, &h.grace, &h.last, &h.created) == nil {
				list = append(list, h)
			}
		}
		rows.Close()
		for _, h := range list {
			ref := h.last
			if ref == "" {
				ref = h.created
			}
			last, err := time.Parse(sqlTime, ref)
			if err != nil {
				continue
			}
			// The ping must arrive by the first scheduled slot after the last one, plus grace.
			sched, err := parser.Parse(h.schedule)
			if err != nil {
				continue
			}
			loc := time.Local
			if h.tz != "" {
				if l, err := time.LoadLocation(h.tz); err == nil {
					loc = l
				}
			}
			due := sched.Next(sched.Next(last.In(loc))).Add(time.Duration(h.grace) * time.Second)
			if time.Now().After(due) {
				_, _ = s.st.DB.ExecContext(ctx, `UPDATE jobs SET overdue = 1 WHERE id = ?`, h.id)
				_, _ = s.st.DB.ExecContext(ctx, `INSERT INTO job_runs (job_id, trigger, status, output, finished_at) VALUES (?, 'watch', 'failed', 'No ping received by the expected time.', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, h.id)
				if s.bus != nil {
					s.bus.Emit(ctx, notify.Event{Category: "cron", Subject: h.name, Severity: notify.Critical, Title: "Heartbeat missed: " + h.name, Message: fmt.Sprintf("No ping since %s. The external job may have stopped.", last.Local().Format("Jan 2 15:04")), Link: "/cron?job=" + h.id})
				}
			}
		}
	}
}

// ---- import / export ----

// CrontabLine renders a job as a crontab entry for people leaving Islet.
func (s *Service) CrontabLine(j *Job) string {
	sched := j.Schedule
	if sched == "" {
		sched = "# manual"
	}
	var cmd string
	switch j.Type {
	case TypeCommand:
		cmd = j.Command
	case TypeScript:
		cmd = s.scriptPath(j.ID)
	case TypeFile:
		cmd = j.Command
	case TypeContainer:
		cmd = fmt.Sprintf("docker exec %s sh -c %s", j.Container, shellQuote(j.Script))
	case TypeImage:
		cmd = "docker run --rm " + j.Command
		if j.Script != "" {
			cmd += " sh -c " + shellQuote(j.Script)
		}
	case TypeHTTP:
		cmd = fmt.Sprintf("curl -fsS -X %s %s", j.HTTPMethod, shellQuote(j.Command))
	default:
		cmd = "# " + j.Type + " jobs have no crontab equivalent"
	}
	if j.Timezone != "" {
		return "CRON_TZ=" + j.Timezone + "\n" + sched + " " + cmd
	}
	return sched + " " + cmd
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ParseCrontab turns crontab text into jobs (disabled, so nothing runs twice).
func ParseCrontab(text string) []Job {
	var out []Job
	tz := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && !strings.ContainsAny(k, " \t*") {
			if k == "CRON_TZ" || k == "TZ" {
				tz = strings.Trim(v, `"'`)
			}
			continue
		}
		f := strings.Fields(line)
		var sched, cmd string
		if strings.HasPrefix(f[0], "@") && len(f) >= 2 {
			sched, cmd = f[0], strings.TrimSpace(strings.TrimPrefix(line, f[0]))
		} else if len(f) >= 6 {
			sched = strings.Join(f[:5], " ")
			cmd = strings.TrimSpace(line[len(sched):])
			// the schedule may have collapsed whitespace; recover the command by position
			idx := 0
			for i := 0; i < 5; i++ {
				idx = strings.Index(line[idx:], f[i]) + idx + len(f[i])
			}
			cmd = strings.TrimSpace(line[idx:])
		} else {
			continue
		}
		if _, err := parser.Parse(sched); err != nil {
			continue
		}
		name := cmd
		if len(name) > 60 {
			name = name[:60]
		}
		name = regexp.MustCompile(`[^A-Za-z0-9 ._-]`).ReplaceAllString(name, " ")
		name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
		if name == "" {
			name = "imported job"
		}
		out = append(out, Job{Name: name, Type: TypeCommand, Schedule: sched, Timezone: tz, Command: cmd, Enabled: false, Overlap: "skip", NotifyOn: "failure", TimeoutSec: 3600})
	}
	return out
}

// ReadSystemCrontabs collects root's crontab and /etc/cron.d entries on Linux.
func ReadSystemCrontabs(ctx context.Context) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	var b strings.Builder
	if out, err := exec.CommandContext(ctx, "crontab", "-l").Output(); err == nil {
		b.Write(out)
		b.WriteString("\n")
	}
	entries, _ := os.ReadDir("/etc/cron.d")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/etc/cron.d", e.Name()))
		if err != nil {
			continue
		}
		// system crontabs carry a user column after the schedule; drop it
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 7 && !strings.HasPrefix(line, "#") && !strings.Contains(f[0], "=") {
				line = strings.Join(append(f[:5], f[6:]...), " ")
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// Lint runs shellcheck when it is installed. ok=false means not installed.
func Lint(ctx context.Context, script string) (output string, ok bool) {
	path, err := exec.LookPath("shellcheck")
	if err != nil {
		return "", false
	}
	cmd := exec.CommandContext(ctx, path, "-f", "gcc", "-s", "bash", "-")
	cmd.Stdin = strings.NewReader(script)
	out, _ := cmd.CombinedOutput()
	return string(out), true
}

func randID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randInt(n int) int {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return int(uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3])) % (n + 1)
}
