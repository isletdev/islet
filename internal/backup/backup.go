// Package backup runs restic inside a container so nothing needs installing
// on the host: encrypted, deduplicated snapshots of Docker volumes, host
// paths, database dumps and Islet's own state, to S3-compatible storage,
// SFTP, a REST server or a local path.
package backup

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/db"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

const resticImage = "restic/restic:0.18.0"

// Destination is a restic repository.
type Destination struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"` // s3 | sftp | local | rest
	Config    map[string]string `json:"config,omitempty"`
	Password  string            `json:"password,omitempty"`
	LastCheck string            `json:"lastCheck"`
	CheckOK   bool              `json:"checkOk"`
	Size      int64             `json:"size"`            // repository size in bytes, updated after each run
	RestoreAt string            `json:"lastRestoreTest"` // last automated restore test
	RestoreOK bool              `json:"restoreTestOk"`
	CreatedAt string            `json:"createdAt"`
	Repo      string            `json:"repo"` // display form, secrets removed
}

// Source is one thing a plan backs up.
type Source struct {
	Type  string `json:"type"`  // volume | path | database | islet
	Value string `json:"value"` // volume name, host path, instance name, or "" for islet
}

// Plan is sources + destination + schedule + retention.
type Plan struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DestinationID string   `json:"destinationId"`
	Sources       []Source `json:"sources"`
	Schedule      string   `json:"schedule"`
	KeepDaily     int      `json:"keepDaily"`
	KeepWeekly    int      `json:"keepWeekly"`
	KeepMonthly   int      `json:"keepMonthly"`
	KeepYearly    int      `json:"keepYearly"`
	Enabled       bool     `json:"enabled"`
	NextRunAt     string   `json:"nextRunAt"`
	LastRunAt     string   `json:"lastRunAt"`
	LastStatus    string   `json:"lastStatus"`
	CreatedAt     string   `json:"createdAt"`
	Described     string   `json:"described"`
	Running       bool     `json:"running"`
	Stale         bool     `json:"stale"`
}

// Run is one execution.
type Run struct {
	ID           int64  `json:"id"`
	PlanID       string `json:"planId"`
	Trigger      string `json:"trigger"`
	Status       string `json:"status"`
	Snapshot     string `json:"snapshot"`
	FilesNew     int64  `json:"filesNew"`
	FilesChanged int64  `json:"filesChanged"`
	BytesAdded   int64  `json:"bytesAdded"`
	BytesTotal   int64  `json:"bytesTotal"`
	Log          string `json:"log,omitempty"`
	Error        string `json:"error"`
	StartedAt    string `json:"startedAt"`
	FinishedAt   string `json:"finishedAt"`
	DurationMs   int64  `json:"durationMs"`
}

// Snapshot is a restic snapshot.
type Snapshot struct {
	ID    string   `json:"id"`
	Time  string   `json:"time"`
	Paths []string `json:"paths"`
	Tags  []string `json:"tags"`
	Size  int64    `json:"size"`
}

// ErrNotFound is returned for unknown plans or destinations.
var ErrNotFound = errors.New("not found")

const sqlTime = "2006-01-02T15:04:05.000Z"

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Service is the backup manager.
type Service struct {
	st      *store.Store
	keys    *auth.Keys
	run     *cmdrun.Runner
	dbs     *db.Service
	bus     *notify.Bus
	log     *slog.Logger
	dataDir string
	mu      sync.Mutex
	active  map[string]context.CancelFunc
}

// New builds the service.
func New(st *store.Store, keys *auth.Keys, run *cmdrun.Runner, dbs *db.Service, bus *notify.Bus, dataDir string, log *slog.Logger) *Service {
	abs, _ := filepath.Abs(dataDir)
	return &Service{st: st, keys: keys, run: run, dbs: dbs, bus: bus, log: log, dataDir: abs, active: map[string]context.CancelFunc{}}
}

// Start runs the scheduler, staleness watch and weekly checks.
func (s *Service) Start(ctx context.Context) {
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE backup_runs SET status = 'failed', error = 'daemon restarted during the run', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE status = 'running'`)
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			s.tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (s *Service) tick(ctx context.Context) {
	plans, err := s.Plans(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(sqlTime)
	for i := range plans {
		p := &plans[i]
		if !p.Enabled || p.Running {
			continue
		}
		if p.NextRunAt != "" && p.NextRunAt <= now {
			go func(id string) { _ = s.RunPlan(context.Background(), "schedule", id, nil) }(p.ID)
		}
		if p.Stale && s.bus != nil {
			s.bus.Emit(ctx, notify.Event{Category: "backup", Severity: notify.Warning, Subject: p.Name, Title: "Backup stale: " + p.Name, Message: "The last successful backup is older than expected. Last: " + p.LastRunAt, Link: "/backups"})
		}
	}
	// Recovery kit: nag once a day while it was never downloaded, and quarterly after.
	if len(plans) > 0 {
		kitAt, _, _ := s.st.Setting(ctx, "backup.kit_downloaded_at")
		lastNag, _, _ := s.st.Setting(ctx, "backup.kit_nag_at")
		due := kitAt == "" || func() bool {
			t, err := time.Parse(time.RFC3339, kitAt)
			return err == nil && time.Since(t) > 90*24*time.Hour
		}()
		nagged := func() bool {
			t, err := time.Parse(time.RFC3339, lastNag)
			return err == nil && time.Since(t) < 24*time.Hour
		}()
		if due && !nagged && s.bus != nil {
			msg := "Download the recovery kit from the Backups page and keep it off this server; without it the encrypted backups cannot be read after the server is gone."
			if kitAt != "" {
				msg = "It has been three months since you saved the recovery kit. Download a fresh copy and check the old one still exists."
			}
			s.bus.Emit(ctx, notify.Event{Category: "backup", Severity: notify.Warning, Title: "Recovery kit reminder", Message: msg, Link: "/backups"})
			_ = s.st.SetSetting(ctx, "backup.kit_nag_at", time.Now().UTC().Format(time.RFC3339))
		}
	}
	dests, _ := s.Destinations(ctx)
	// Monthly restore tests: pull a small path out of the latest snapshot.
	for _, d := range dests {
		v, _, _ := s.st.Setting(ctx, "backup.restoretest."+d.ID)
		at, _, _ := strings.Cut(v, " ")
		if t, err := time.Parse(time.RFC3339, at); v == "" || err != nil || time.Since(t) > 30*24*time.Hour {
			if d.LastCheck == "" {
				continue // no successful backup yet
			}
			go func(id string) { _, _ = s.RestoreTest(context.Background(), "system", id) }(d.ID)
		}
	}
	// Weekly repository checks.
	for _, d := range dests {
		if last, err := time.Parse(time.RFC3339, d.LastCheck); d.LastCheck == "" || err != nil || time.Since(last) > 7*24*time.Hour {
			if d.LastCheck == "" && time.Since(mustTime(d.CreatedAt)) < time.Hour {
				continue // just created; the first backup will populate it
			}
			go func(id string) { _, _ = s.Verify(context.Background(), "system", id) }(d.ID)
		}
	}
}

func mustTime(s string) time.Time {
	t, _ := time.Parse(sqlTime, s)
	return t
}

// ---- destinations ----

// Validate checks a destination config.
func (d *Destination) Validate() error {
	d.Name = strings.ToLower(strings.TrimSpace(d.Name))
	if !nameRe.MatchString(d.Name) {
		return errors.New("name must be 1-40 lowercase letters, digits or dashes")
	}
	need := func(keys ...string) error {
		for _, k := range keys {
			if strings.TrimSpace(d.Config[k]) == "" {
				return fmt.Errorf("%s is required", k)
			}
		}
		return nil
	}
	switch d.Type {
	case "s3":
		if err := need("endpoint", "bucket", "accessKey", "secretKey"); err != nil {
			return err
		}
		d.Config["endpoint"] = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(d.Config["endpoint"], "https://"), "http://"), "/")
	case "sftp":
		if err := need("host", "user", "path", "privateKey"); err != nil {
			return err
		}
		if !strings.Contains(d.Config["privateKey"], "PRIVATE KEY") {
			return errors.New("privateKey must be an OpenSSH private key")
		}
	case "local":
		if err := need("path"); err != nil {
			return err
		}
		if !filepath.IsAbs(d.Config["path"]) {
			return errors.New("path must be absolute")
		}
	case "rest":
		if err := need("url"); err != nil {
			return err
		}
	default:
		return errors.New("type must be s3, sftp, local or rest")
	}
	return nil
}

func (d *Destination) display() string {
	c := d.Config
	switch d.Type {
	case "s3":
		return "s3:" + c["endpoint"] + "/" + c["bucket"] + "/" + strings.Trim(c["prefix"], "/")
	case "sftp":
		return "sftp:" + c["user"] + "@" + c["host"] + ":" + c["path"]
	case "local":
		return "local:" + c["path"]
	case "rest":
		return "rest:" + strings.TrimRight(c["url"], "/")
	}
	return ""
}

// stSetting is a tiny shim so scanDest can read settings without a context.
func (s *Service) stSetting(key string) (string, bool, error) {
	return s.st.Setting(context.Background(), key)
}

func (s *Service) scanDest(sc interface{ Scan(...any) error }) (Destination, error) {
	var d Destination
	var cfg, pw []byte
	err := sc.Scan(&d.ID, &d.Name, &d.Type, &cfg, &pw, &d.LastCheck, &d.CheckOK, &d.CreatedAt)
	if err != nil {
		return d, err
	}
	d.Config = map[string]string{}
	if b, err := s.keys.Decrypt(cfg); err == nil {
		_ = json.Unmarshal(b, &d.Config)
	}
	if b, err := s.keys.Decrypt(pw); err == nil {
		d.Password = string(b)
	}
	d.Repo = d.display()
	if v, _, _ := s.stSetting("backup.size." + d.ID); v != "" {
		d.Size, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, _, _ := s.stSetting("backup.restoretest." + d.ID); v != "" {
		at, ok, _ := strings.Cut(v, " ")
		d.RestoreAt, d.RestoreOK = at, ok == "ok"
	}
	return d, nil
}

// Destinations lists repositories (secrets included; the API redacts).
func (s *Service) Destinations(ctx context.Context) ([]Destination, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, name, type, config, password, last_check, check_ok, created_at FROM backup_destinations WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Destination{}
	for rows.Next() {
		d, err := s.scanDest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Destination returns one repository.
func (s *Service) Destination(ctx context.Context, id string) (*Destination, error) {
	d, err := s.scanDest(s.st.DB.QueryRowContext(ctx, `SELECT id, name, type, config, password, last_check, check_ok, created_at FROM backup_destinations WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

// SaveDestination stores a repository and initialises it.
func (s *Service) SaveDestination(ctx context.Context, actor string, d *Destination) (*Destination, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	isNew := d.ID == ""
	if isNew {
		d.ID = randHex(6)
		if d.Password == "" {
			d.Password = randHex(24)
		}
	} else {
		old, err := s.Destination(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		d.Password = old.Password // the key never changes after init
		for k, v := range old.Config {
			if d.Config[k] == "" && (k == "secretKey" || k == "privateKey" || k == "password") {
				d.Config[k] = v
			}
		}
	}
	cfg, _ := json.Marshal(d.Config)
	encCfg, err := s.keys.Encrypt(cfg)
	if err != nil {
		return nil, err
	}
	encPw, err := s.keys.Encrypt([]byte(d.Password))
	if err != nil {
		return nil, err
	}
	// Try to reach and initialise the repository before saving.
	if out, err := s.restic(ctx, actor, d, nil, "cat", "config"); err != nil {
		if strings.Contains(out, "Is there a repository at the following location") || strings.Contains(out, "does not exist") || strings.Contains(out, "no such file") || strings.Contains(out, "unable to open config") {
			if out2, err2 := s.restic(ctx, actor, d, nil, "init"); err2 != nil {
				return nil, errors.New("could not initialise the repository: " + lastLine(out2))
			}
		} else {
			return nil, errors.New("could not reach the repository: " + lastLine(out))
		}
	}
	if isNew {
		_, err = s.st.DB.ExecContext(ctx, `INSERT INTO backup_destinations (id, server_id, name, type, config, password) VALUES (?, ?, ?, ?, ?, ?)`, d.ID, s.st.ServerID, d.Name, d.Type, encCfg, encPw)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("a destination with that name already exists")
		}
	} else {
		_, err = s.st.DB.ExecContext(ctx, `UPDATE backup_destinations SET config = ? WHERE id = ?`, encCfg, d.ID)
	}
	if err != nil {
		return nil, err
	}
	_ = s.st.Audit(ctx, actor, "backup.destination", d.ID, d.Name+" ("+d.Type+")")
	return s.Destination(ctx, d.ID)
}

// DeleteDestination removes a repository record (data stays where it is).
func (s *Service) DeleteDestination(ctx context.Context, id string) error {
	var n int
	_ = s.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM backup_plans WHERE destination_id = ?`, id).Scan(&n)
	if n > 0 {
		return errors.New("plans still use this destination; delete them first")
	}
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM backup_destinations WHERE id = ? AND server_id = ?`, id, s.st.ServerID)
	return err
}

// ---- plans ----

// Validate checks a plan.
func (p *Plan) Validate() error {
	p.Name = strings.ToLower(strings.TrimSpace(p.Name))
	if !nameRe.MatchString(p.Name) {
		return errors.New("name must be 1-40 lowercase letters, digits or dashes")
	}
	if p.DestinationID == "" {
		return errors.New("pick a destination")
	}
	if len(p.Sources) == 0 {
		return errors.New("pick at least one source")
	}
	for i, src := range p.Sources {
		src.Value = strings.TrimSpace(src.Value)
		switch src.Type {
		case "volume":
			if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(src.Value) {
				return errors.New("invalid volume name " + src.Value)
			}
		case "path":
			if !filepath.IsAbs(src.Value) || strings.Contains(src.Value, "..") {
				return errors.New("paths must be absolute: " + src.Value)
			}
		case "database":
			if src.Value == "" {
				return errors.New("database source needs an instance name")
			}
		case "islet":
		default:
			return errors.New("unknown source type " + src.Type)
		}
		p.Sources[i] = src
	}
	if p.Schedule == "" {
		p.Schedule = "0 3 * * *"
	}
	if _, _, err := cron.Preview(p.Schedule, "", 1); err != nil {
		return err
	}
	if p.KeepDaily < 0 || p.KeepWeekly < 0 || p.KeepMonthly < 0 || p.KeepYearly < 0 || p.KeepDaily+p.KeepWeekly+p.KeepMonthly+p.KeepYearly == 0 {
		return errors.New("retention must keep at least one snapshot")
	}
	return nil
}

const planCols = `id, name, destination_id, sources, schedule, keep_daily, keep_weekly, keep_monthly, keep_yearly, enabled, next_run_at, last_run_at, last_status, created_at`

func scanPlan(sc interface{ Scan(...any) error }) (Plan, error) {
	var p Plan
	var src string
	err := sc.Scan(&p.ID, &p.Name, &p.DestinationID, &src, &p.Schedule, &p.KeepDaily, &p.KeepWeekly, &p.KeepMonthly, &p.KeepYearly, &p.Enabled, &p.NextRunAt, &p.LastRunAt, &p.LastStatus, &p.CreatedAt)
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(src), &p.Sources)
	if p.Sources == nil {
		p.Sources = []Source{}
	}
	return p, nil
}

func (s *Service) decorate(p *Plan) {
	p.Described = cron.Describe(p.Schedule)
	s.mu.Lock()
	_, p.Running = s.active[p.ID]
	s.mu.Unlock()
	// Stale: no success within twice the schedule interval (at least 26 hours).
	if p.Enabled && p.LastRunAt != "" {
		_, next, err := cron.Preview(p.Schedule, "", 2)
		interval := 24 * time.Hour
		if err == nil && len(next) == 2 {
			interval = next[1].Sub(next[0])
		}
		if last, err := time.Parse(sqlTime, p.LastRunAt); err == nil && p.LastStatus == "success" {
			p.Stale = time.Since(last) > max(2*interval, 26*time.Hour)
		} else if p.LastStatus == "failed" {
			p.Stale = true
		}
	}
}

// Plans lists plans.
func (s *Service) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+planCols+` FROM backup_plans WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		s.decorate(&p)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Plan returns one plan.
func (s *Service) Plan(ctx context.Context, id string) (*Plan, error) {
	p, err := scanPlan(s.st.DB.QueryRowContext(ctx, `SELECT `+planCols+` FROM backup_plans WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.decorate(&p)
	return &p, nil
}

// SavePlan inserts or updates a plan and computes its next run.
func (s *Service) SavePlan(ctx context.Context, actor string, p *Plan) (*Plan, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.Destination(ctx, p.DestinationID); err != nil {
		return nil, errors.New("destination not found")
	}
	src, _ := json.Marshal(p.Sources)
	next := ""
	if p.Enabled {
		if _, n, err := cron.Preview(p.Schedule, "", 1); err == nil && len(n) == 1 {
			next = n[0].UTC().Format(sqlTime)
		}
	}
	if p.ID == "" {
		p.ID = randHex(6)
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO backup_plans (id, server_id, name, destination_id, sources, schedule, keep_daily, keep_weekly, keep_monthly, keep_yearly, enabled, next_run_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, s.st.ServerID, p.Name, p.DestinationID, string(src), p.Schedule, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, p.KeepYearly, p.Enabled, next)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("a plan with that name already exists")
			}
			return nil, err
		}
	} else {
		if _, err := s.Plan(ctx, p.ID); err != nil {
			return nil, err
		}
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE backup_plans SET name = ?, destination_id = ?, sources = ?, schedule = ?, keep_daily = ?, keep_weekly = ?, keep_monthly = ?, keep_yearly = ?, enabled = ?, next_run_at = ? WHERE id = ?`,
			p.Name, p.DestinationID, string(src), p.Schedule, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, p.KeepYearly, p.Enabled, next, p.ID); err != nil {
			return nil, err
		}
	}
	_ = s.st.Audit(ctx, actor, "backup.plan", p.ID, p.Name)
	return s.Plan(ctx, p.ID)
}

// DeletePlan removes a plan and its runs. Snapshots stay in the repository.
func (s *Service) DeletePlan(ctx context.Context, id string) error {
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM backup_runs WHERE plan_id = ?`, id)
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM backup_plans WHERE id = ? AND server_id = ?`, id, s.st.ServerID)
	return err
}

// Runs lists runs of a plan without logs.
func (s *Service) Runs(ctx context.Context, planID string, limit int) ([]Run, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, plan_id, trigger, status, snapshot, files_new, files_changed, bytes_added, bytes_total, error, started_at, finished_at, duration_ms FROM backup_runs WHERE plan_id = ? ORDER BY id DESC LIMIT ?`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.PlanID, &r.Trigger, &r.Status, &r.Snapshot, &r.FilesNew, &r.FilesChanged, &r.BytesAdded, &r.BytesTotal, &r.Error, &r.StartedAt, &r.FinishedAt, &r.DurationMs); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// RunDetail returns one run with its log.
func (s *Service) RunDetail(ctx context.Context, planID string, id int64) (*Run, error) {
	var r Run
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, plan_id, trigger, status, snapshot, files_new, files_changed, bytes_added, bytes_total, log, error, started_at, finished_at, duration_ms FROM backup_runs WHERE plan_id = ? AND id = ?`, planID, id).
		Scan(&r.ID, &r.PlanID, &r.Trigger, &r.Status, &r.Snapshot, &r.FilesNew, &r.FilesChanged, &r.BytesAdded, &r.BytesTotal, &r.Log, &r.Error, &r.StartedAt, &r.FinishedAt, &r.DurationMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// ---- restic ----

// resticArgs builds the docker run command for a repository, plus mounts.
func (s *Service) resticArgs(d *Destination, mounts []string, extraEnv []string) ([]string, func(), error) {
	sockCleanup := func() {}
	args := []string{"run", "--rm", "--name", "islet-restic-" + randHex(3), "-e", "RESTIC_PASSWORD=" + d.Password, "-e", "RESTIC_CACHE_DIR=/cache", "-v", "islet-restic-cache:/cache", "--hostname", s.st.Hostname}
	c := d.Config
	switch d.Type {
	case "s3":
		repo := "s3:https://" + c["endpoint"] + "/" + c["bucket"]
		if p := strings.Trim(c["prefix"], "/"); p != "" {
			repo += "/" + p
		}
		args = append(args, "-e", "RESTIC_REPOSITORY="+repo, "-e", "AWS_ACCESS_KEY_ID="+c["accessKey"], "-e", "AWS_SECRET_ACCESS_KEY="+c["secretKey"])
		if c["region"] != "" {
			args = append(args, "-e", "AWS_DEFAULT_REGION="+c["region"])
		}
	case "sftp":
		keyFile, err := os.CreateTemp(s.dataDir, "sftp-key-*")
		if err != nil {
			return nil, nil, err
		}
		_, _ = keyFile.WriteString(strings.TrimSpace(c["privateKey"]) + "\n")
		keyFile.Close()
		_ = os.Chmod(keyFile.Name(), 0o600)
		sockCleanup = func() { os.Remove(keyFile.Name()) }
		port := c["port"]
		if port == "" {
			port = "22"
		}
		args = append(args, "-v", keyFile.Name()+":/root/.ssh/id_key:ro", "-e", "RESTIC_REPOSITORY=sftp:"+c["user"]+"@"+c["host"]+":"+c["path"],
			"-e", "RESTIC_SFTP_COMMAND=ssh -p "+port+" -i /root/.ssh/id_key -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/cache/known_hosts "+c["user"]+"@"+c["host"]+" -s sftp")
	case "local":
		_ = os.MkdirAll(c["path"], 0o750)
		args = append(args, "-v", c["path"]+":/repo", "-e", "RESTIC_REPOSITORY=/repo")
	case "rest":
		args = append(args, "-e", "RESTIC_REPOSITORY=rest:"+strings.TrimRight(c["url"], "/"))
		if c["user"] != "" {
			args = append(args, "-e", "RESTIC_REST_USERNAME="+c["user"], "-e", "RESTIC_REST_PASSWORD="+c["password"])
		}
	}
	for _, m := range mounts {
		args = append(args, "-v", m)
	}
	for _, e := range extraEnv {
		args = append(args, "-e", e)
	}
	args = append(args, resticImage)
	return args, sockCleanup, nil
}

// restic runs a command and returns its combined output.
func (s *Service) restic(ctx context.Context, actor string, d *Destination, mounts []string, cmdArgs ...string) (string, error) {
	args, cleanup, err := s.resticArgs(d, mounts, nil)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args = append(args, cmdArgs...)
	res, err := s.run.Run(ctx, actor, "docker", args...)
	out := res.Stdout + res.Stderr
	if err != nil {
		var ce *cmdrun.Error
		if errors.As(err, &ce) {
			return ce.Result.Stdout + ce.Result.Stderr, err
		}
		return out, err
	}
	return out, nil
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return "no output"
}

// RunPlan executes a plan now. Output goes to w as it happens when given.
func (s *Service) RunPlan(ctx context.Context, trigger, id string, w io.Writer) error {
	p, err := s.Plan(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, busy := s.active[id]; busy {
		s.mu.Unlock()
		return errors.New("this plan is already running")
	}
	rctx, cancel := context.WithCancel(ctx)
	s.active[id] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
	}()
	d, err := s.Destination(ctx, p.DestinationID)
	if err != nil {
		return err
	}
	res, err := s.st.DB.ExecContext(ctx, `INSERT INTO backup_runs (plan_id, trigger, status) VALUES (?, ?, 'running')`, p.ID, trigger)
	if err != nil {
		return err
	}
	runID, _ := res.LastInsertId()
	start := time.Now()
	var log strings.Builder
	say := func(l string) {
		log.WriteString(l + "\n")
		if w != nil {
			fmt.Fprintln(w, l)
		}
	}
	finish := func(status, snapshot, errMsg string, sum *summary) {
		var fn, fc, ba, bt int64
		if sum != nil {
			fn, fc, ba, bt = sum.FilesNew, sum.FilesChanged, sum.DataAdded, sum.TotalBytes
		}
		_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE backup_runs SET status = ?, snapshot = ?, files_new = ?, files_changed = ?, bytes_added = ?, bytes_total = ?, log = ?, error = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), duration_ms = ? WHERE id = ?`,
			status, snapshot, fn, fc, ba, bt, log.String(), errMsg, time.Since(start).Milliseconds(), runID)
		next := ""
		if p.Enabled {
			if _, n, err := cron.Preview(p.Schedule, "", 1); err == nil && len(n) == 1 {
				next = n[0].UTC().Format(sqlTime)
			}
		}
		_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE backup_plans SET last_run_at = ?, last_status = ?, next_run_at = ? WHERE id = ?`, time.Now().UTC().Format(sqlTime), status, next, p.ID)
		if s.bus != nil {
			if status == "failed" {
				s.bus.Emit(context.Background(), notify.Event{Category: "backup", Severity: notify.Warning, Subject: p.Name, Title: "Backup failed: " + p.Name, Message: errMsg, Link: "/backups"})
			} else if sum != nil && p.LastStatus == "failed" {
				s.bus.Emit(context.Background(), notify.Event{Category: "backup", Severity: notify.Info, Subject: p.Name, Title: "Backup failed: " + p.Name, Message: fmt.Sprintf("Recovered: snapshot %s, %s added.", snapshot, human(sum.DataAdded)), Link: "/backups"})
			}
		}
	}

	say(fmt.Sprintf("[islet] backup plan %s → %s", p.Name, d.Repo))
	// Stage: mounts for each source. Everything lands under /data inside the container.
	var mounts []string
	dumpDirs := map[string]bool{}
	for _, src := range p.Sources {
		switch src.Type {
		case "volume":
			mounts = append(mounts, src.Value+":/data/volumes/"+src.Value+":ro")
		case "path":
			mounts = append(mounts, src.Value+":/data/paths"+filepath.ToSlash(src.Value)+":ro")
		case "database":
			if s.dbs == nil {
				finish("failed", "", "database sources are unavailable", nil)
				return errors.New("database sources are unavailable")
			}
			inst, err := s.dbs.Get(rctx, "backup", src.Value)
			if err != nil {
				say("[islet] database " + src.Value + " not found, skipping")
				continue
			}
			say("[islet] dumping " + inst.Name)
			names := []string{inst.Database}
			if inst.Engine == "redis" {
				names = []string{"redis"}
			} else if list, err := s.dbs.Databases(rctx, "backup", inst); err == nil {
				names = names[:0]
				for _, x := range list {
					if x.Name != "postgres" && x.Name != "admin" && x.Name != "local" && x.Name != "config" {
						names = append(names, x.Name)
					}
				}
			}
			for _, n := range names {
				if _, err := s.dbs.DumpNow(rctx, "backup", inst, n); err != nil {
					say("[islet] dump " + n + " failed: " + err.Error())
					finish("failed", "", "dump of "+n+" failed: "+err.Error(), nil)
					return err
				}
			}
			dir := filepath.Join(s.dataDir, "dumps", inst.Name)
			if !dumpDirs[dir] {
				dumpDirs[dir] = true
				mounts = append(mounts, dir+":/data/databases/"+inst.Name+":ro")
			}
		case "islet":
			mounts = append(mounts, s.dataDir+":/data/islet:ro")
		}
	}
	if len(mounts) == 0 {
		finish("failed", "", "nothing to back up", nil)
		return errors.New("nothing to back up")
	}
	// Backup with JSON progress; the summary line carries the stats.
	args, cleanup, err := s.resticArgs(d, mounts, nil)
	if err != nil {
		finish("failed", "", err.Error(), nil)
		return err
	}
	defer cleanup()
	excl := []string{"--exclude", "/data/islet/trash", "--exclude", "/data/islet/apps/*/src", "--exclude", "/data/islet/bin", "--exclude", "**/node_modules/.cache", "--exclude", "**/.cache"}
	args = append(args, "backup", "/data", "--json", "--tag", "islet", "--tag", "plan:"+p.Name)
	args = append(args, excl...)
	cmd := exec.CommandContext(rctx, "docker", args...)
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		finish("failed", "", err.Error(), nil)
		return err
	}
	var sum *summary
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	lastPct := -1
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m["message_type"] {
		case "status":
			if pct, ok := m["percent_done"].(float64); ok {
				p := int(pct * 100)
				if p/10 != lastPct/10 {
					lastPct = p
					say(fmt.Sprintf("  %d%% · %s processed", p, human(int64(num(m["bytes_done"])))))
				}
			}
		case "summary":
			sum = &summary{FilesNew: int64(num(m["files_new"])), FilesChanged: int64(num(m["files_changed"])), DataAdded: int64(num(m["data_added"])), TotalBytes: int64(num(m["total_bytes_processed"])), SnapshotID: str(m["snapshot_id"])}
		case "error":
			say("  " + str(m["error"]) + " " + str(m["item"]))
		}
	}
	err = cmd.Wait()
	s.run.Record(ctx, "backup", "docker run restic backup /data (plan "+p.Name+")", cmdrun.Result{ExitCode: exitCode(err), Duration: time.Since(start), Stderr: stderr.String()})
	if err != nil && sum == nil {
		msg := lastLine(stderr.String())
		if rctx.Err() != nil {
			msg = "cancelled"
		}
		say("[islet] failed: " + msg)
		finish("failed", "", msg, nil)
		return errors.New(msg)
	}
	if sum == nil {
		finish("failed", "", "restic produced no summary", nil)
		return errors.New("restic produced no summary")
	}
	snap := sum.SnapshotID
	if len(snap) > 8 {
		snap = snap[:8]
	}
	say(fmt.Sprintf("[islet] snapshot %s: %d new files, %d changed, %s added, %s total", snap, sum.FilesNew, sum.FilesChanged, human(sum.DataAdded), human(sum.TotalBytes)))
	// Retention.
	say(fmt.Sprintf("[islet] forget: keep %d daily, %d weekly, %d monthly, %d yearly", p.KeepDaily, p.KeepWeekly, p.KeepMonthly, p.KeepYearly))
	if out, err := s.restic(rctx, "backup", d, nil, "forget", "--tag", "plan:"+p.Name, "--group-by", "tags", "--keep-last", "3", "--keep-daily", strconv.Itoa(p.KeepDaily), "--keep-weekly", strconv.Itoa(p.KeepWeekly), "--keep-monthly", strconv.Itoa(p.KeepMonthly), "--keep-yearly", strconv.Itoa(p.KeepYearly), "--prune", "--quiet"); err != nil {
		say("[islet] forget failed (snapshot is safe): " + lastLine(out))
	}
	finish("success", snap, "", sum)
	if out, err := s.restic(rctx, "backup", d, nil, "stats", "--json", "--mode", "raw-data"); err == nil {
		var st struct {
			TotalSize int64 `json:"total_size"`
		}
		if i := strings.Index(out, "{"); i >= 0 && json.Unmarshal([]byte(out[i:]), &st) == nil {
			_ = s.st.SetSetting(context.Background(), "backup.size."+d.ID, strconv.FormatInt(st.TotalSize, 10))
			say(fmt.Sprintf("[islet] repository now holds %s", human(st.TotalSize)))
		}
	}
	say(fmt.Sprintf("[islet] done in %s", time.Since(start).Round(time.Second)))
	return nil
}

type summary struct {
	FilesNew, FilesChanged, DataAdded, TotalBytes int64
	SnapshotID                                    string
}

func num(v any) float64 { f, _ := v.(float64); return f }
func str(v any) string  { s, _ := v.(string); return s }

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// Cancel stops a running plan.
func (s *Service) Cancel(id string) bool {
	s.mu.Lock()
	cancel, ok := s.active[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Snapshots lists a destination's snapshots, newest first.
func (s *Service) Snapshots(ctx context.Context, actor, destID, plan string) ([]Snapshot, error) {
	d, err := s.Destination(ctx, destID)
	if err != nil {
		return nil, err
	}
	args := []string{"snapshots", "--json"}
	if plan != "" {
		args = append(args, "--tag", "plan:"+plan)
	}
	out, err := s.restic(ctx, actor, d, nil, args...)
	if err != nil {
		return nil, errors.New(lastLine(out))
	}
	var raw []struct {
		ID    string   `json:"id"`
		Time  string   `json:"time"`
		Paths []string `json:"paths"`
		Tags  []string `json:"tags"`
		Sum   *struct {
			TotalBytes int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if i := strings.Index(out, "["); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, errors.New("could not parse snapshots")
	}
	list := []Snapshot{}
	for _, r := range raw {
		sn := Snapshot{ID: r.ID[:8], Time: r.Time, Paths: r.Paths, Tags: r.Tags}
		if r.Sum != nil {
			sn.Size = r.Sum.TotalBytes
		}
		list = append(list, sn)
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

// Ls lists a directory inside a snapshot.
func (s *Service) Ls(ctx context.Context, actor, destID, snapshot, path string) ([]map[string]any, error) {
	d, err := s.Destination(ctx, destID)
	if err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[0-9a-f]{8,64}$|^latest$`).MatchString(snapshot) {
		return nil, errors.New("invalid snapshot id")
	}
	if path == "" {
		path = "/data"
	}
	out, err := s.restic(ctx, actor, d, nil, "ls", "--json", snapshot, path)
	if err != nil {
		return nil, errors.New(lastLine(out))
	}
	entries := []map[string]any{}
	for _, l := range strings.Split(out, "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil || m["struct_type"] == "snapshot" || m["message_type"] == "snapshot" {
			continue
		}
		if p, _ := m["path"].(string); p != "" && filepath.ToSlash(filepath.Dir(p)) == strings.TrimRight(path, "/") {
			entries = append(entries, map[string]any{"path": p, "name": m["name"], "type": m["type"], "size": m["size"], "mtime": m["mtime"]})
		}
	}
	return entries, nil
}

// Restore extracts part of a snapshot. Volume targets restore into a new
// Docker volume; everything else lands under <data>/restore/<time>/.
func (s *Service) Restore(ctx context.Context, actor, destID, snapshot, include, newVolume string) (string, error) {
	d, err := s.Destination(ctx, destID)
	if err != nil {
		return "", err
	}
	if !regexp.MustCompile(`^[0-9a-f]{8,64}$|^latest$`).MatchString(snapshot) {
		return "", errors.New("invalid snapshot id")
	}
	if include == "" || !strings.HasPrefix(include, "/data/") {
		return "", errors.New("pick a path inside the snapshot")
	}
	var mounts []string
	target := ""
	if newVolume != "" {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(newVolume) {
			return "", errors.New("invalid volume name")
		}
		if out, err := s.run.Run(ctx, actor, "docker", "volume", "create", newVolume); err != nil {
			return "", errors.New(lastLine(out.Stderr))
		}
		mounts = []string{newVolume + ":/restore"}
		target = "volume " + newVolume
	} else {
		dir := filepath.Join(s.dataDir, "restore", time.Now().UTC().Format("20060102-150405"))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", err
		}
		mounts = []string{dir + ":/restore"}
		target = dir
	}
	// A volume restores at the new volume's root. Files and folders keep
	// their name under the restore directory.
	inc := strings.TrimRight(include, "/")
	var args []string
	if newVolume != "" {
		args = []string{"restore", snapshot + ":" + inc, "--target", "/restore"}
	} else {
		parent := inc[:strings.LastIndex(inc, "/")]
		args = []string{"restore", snapshot + ":" + parent, "--include", "/" + inc[strings.LastIndex(inc, "/")+1:], "--target", "/restore"}
	}
	out, err := s.restic(ctx, actor, d, mounts, args...)
	if err != nil {
		return "", errors.New(lastLine(out))
	}
	_ = s.st.Audit(ctx, actor, "backup.restore", snapshot, include+" → "+target)
	return target, nil
}

// Verify runs restic check on a destination and records the result.
func (s *Service) Verify(ctx context.Context, actor, destID string) (string, error) {
	d, err := s.Destination(ctx, destID)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	out, err := s.restic(cctx, actor, d, nil, "check", "--read-data-subset=5%")
	ok := err == nil
	_, _ = s.st.DB.ExecContext(context.Background(), `UPDATE backup_destinations SET last_check = ?, check_ok = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), ok, d.ID)
	if !ok {
		if s.bus != nil {
			s.bus.Emit(context.Background(), notify.Event{Category: "backup", Severity: notify.Critical, Title: "Backup repository check failed: " + d.Name, Message: lastLine(out), Link: "/backups"})
		}
		return out, errors.New(lastLine(out))
	}
	return out, nil
}

// RestoreTest restores the smallest useful path from the latest snapshot
// into a scratch directory, checks that files came back, and records the
// result. It proves the repository, the key and the credentials all work.
func (s *Service) RestoreTest(ctx context.Context, actor, destID string) (string, error) {
	d, err := s.Destination(ctx, destID)
	if err != nil {
		return "", err
	}
	record := func(ok bool, msg string) {
		state := "failed"
		if ok {
			state = "ok"
		}
		_ = s.st.SetSetting(context.Background(), "backup.restoretest."+d.ID, time.Now().UTC().Format(time.RFC3339)+" "+state)
		if !ok && s.bus != nil {
			s.bus.Emit(context.Background(), notify.Event{Category: "backup", Severity: notify.Critical, Title: "Restore test failed: " + d.Name, Message: msg, Link: "/backups"})
		}
	}
	snaps, err := s.Snapshots(ctx, actor, destID, "")
	if err != nil {
		record(false, err.Error())
		return "", err
	}
	if len(snaps) == 0 {
		return "no snapshots yet", nil
	}
	// Prefer Islet state, then database dumps, then whatever the snapshot holds.
	include := ""
	for _, cand := range []string{"/data/islet/islet.db", "/data/databases", "/data/islet"} {
		if entries, err := s.Ls(ctx, actor, destID, "latest", filepath.ToSlash(filepath.Dir(cand))); err == nil {
			for _, e := range entries {
				if p, _ := e["path"].(string); p == cand {
					include = cand
					break
				}
			}
		}
		if include != "" {
			break
		}
	}
	if include == "" && len(snaps[0].Paths) > 0 {
		include = snaps[0].Paths[0]
	}
	if include == "" {
		return "nothing to test", nil
	}
	dir := filepath.Join(s.dataDir, "restore-test")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	inc := strings.TrimRight(include, "/")
	parent := inc[:strings.LastIndex(inc, "/")]
	out, err := s.restic(cctx, actor, d, []string{dir + ":/restore"}, "restore", "latest:"+parent, "--include", "/"+inc[strings.LastIndex(inc, "/")+1:], "--target", "/restore")
	if err != nil {
		record(false, lastLine(out))
		return out, errors.New(lastLine(out))
	}
	var files int
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files++
		}
		return nil
	})
	if files == 0 {
		record(false, "restore produced no files for "+include)
		return out, errors.New("restore produced no files")
	}
	record(true, "")
	_ = s.st.Audit(ctx, actor, "backup.restoretest", d.ID, fmt.Sprintf("%s: %d files", include, files))
	return fmt.Sprintf("restored %d file(s) from %s", files, include), nil
}

// RecoveryKit is everything needed to restore without this server.
func (s *Service) RecoveryKit(ctx context.Context) ([]byte, error) {
	dests, err := s.Destinations(ctx)
	if err != nil {
		return nil, err
	}
	plans, _ := s.Plans(ctx)
	kit := map[string]any{
		"islet":        "recovery kit",
		"generatedAt":  time.Now().UTC().Format(time.RFC3339),
		"hostname":     s.st.Hostname,
		"destinations": dests,
		"plans":        plans,
		"howToRestore": []string{
			"1. Install restic (or use docker run --rm restic/restic).",
			"2. For each destination set RESTIC_REPOSITORY (see repo), RESTIC_PASSWORD (password) and the provider credentials from config.",
			"3. restic snapshots            # list what exists",
			"4. restic restore latest --target ./restore   # or a snapshot id; add --include /data/volumes/NAME for one volume",
			"5. Volumes are under /data/volumes/<name>, database dumps under /data/databases/<instance>, Islet state under /data/islet.",
			"6. On a fresh Islet install, copy /data/islet/* back into /var/lib/islet before starting isletd, then docker volume create + copy for each volume.",
		},
	}
	return json.MarshalIndent(kit, "", "  ")
}

// VolumeNames lists Docker volumes for the source picker.
func (s *Service) VolumeNames(ctx context.Context) []string {
	res, err := s.run.Run(ctx, "system", "docker", "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if l != "" && !strings.HasPrefix(l, "islet-restic-") && !strings.HasPrefix(l, "islet-trivy-") && !strings.HasPrefix(l, "buildx_") {
			out = append(out, l)
		}
	}
	return out
}

// HasPlan reports whether any enabled plan exists (for the Security Score).
func (s *Service) HasPlan(ctx context.Context) bool {
	var n int
	_ = s.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM backup_plans WHERE enabled = 1 AND server_id = ?`, s.st.ServerID).Scan(&n)
	return n > 0
}

// Health summarises backups for the dashboard.
func (s *Service) Health(ctx context.Context) map[string]any {
	plans, _ := s.Plans(ctx)
	dests, _ := s.Destinations(ctx)
	last, next := "", ""
	stale, failed := 0, 0
	for _, p := range plans {
		if p.LastRunAt > last && p.LastStatus == "success" {
			last = p.LastRunAt
		}
		if p.Enabled && (next == "" || p.NextRunAt < next) && p.NextRunAt != "" {
			next = p.NextRunAt
		}
		if p.Stale {
			stale++
		}
		if p.LastStatus == "failed" {
			failed++
		}
	}
	verified, restored := "", ""
	var size int64
	for _, d := range dests {
		if d.CheckOK && d.LastCheck > verified {
			verified = d.LastCheck
		}
		if d.RestoreOK && d.RestoreAt > restored {
			restored = d.RestoreAt
		}
		size += d.Size
	}
	return map[string]any{"plans": len(plans), "destinations": len(dests), "lastSuccess": last, "nextRun": next, "stale": stale, "failed": failed, "lastVerified": verified, "lastRestoreTest": restored, "size": size}
}

func human(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + " " + units[i]
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ = runtime.GOOS
