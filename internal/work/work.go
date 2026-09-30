// Package work is the queue for work that takes longer than a request.
//
// Everything this daemon did until now was either fast enough to answer inside
// the request that asked for it, or a run belonging to one feature with a table
// of its own: a deploy, a backup run, an assistant run. Transcoding a video is
// neither. It is minutes of one core, it fails halfway, somebody wants to see
// how far along it is, and on a 1 vCPU server two at once is the difference
// between a slow box and an unreachable one.
//
// What this is, therefore, is deliberately small: one table, one worker, one task
// at a time, and a handler registered per kind. There are no priorities, no
// dependencies, no fan-out and no schedule — cron is next door and already does
// the last one. Each of those is a thing to add when something actually needs
// it, and the thing that needs a queue today needs a line.
package work

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// The states a job can be in. A job is queued, then running, then exactly one
// of the three endings — and an ending is final: nothing is retried behind the
// person's back, because the failures this queue sees are things like a codec
// the file does not have, which retrying only repeats.
const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"
)

// ErrNoTask is a job that is not on this server.
var ErrNoTask = errors.New("no such task")

// Task is one piece of work.
type Task struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	// Detail is what it is doing right now, in words, for the person watching.
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
	// Subject is what the job is about — "media:object:<id>" — so the panel can
	// show it beside that thing instead of in a list of identifiers. Free text:
	// nothing here parses it.
	Subject string `json:"subject,omitempty"`
	Label   string `json:"label"`
	Actor   string `json:"actor,omitempty"`
	// Payload is the job's own arguments. Opaque here on purpose: the queue
	// knows nothing about media, and media knows nothing about SQL.
	Payload    string `json:"-"`
	CreatedAt  string `json:"createdAt"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// Args decodes the payload a job was created with.
func (j Task) Args(into any) error {
	if strings.TrimSpace(j.Payload) == "" {
		return nil
	}
	return json.Unmarshal([]byte(j.Payload), into)
}

// Live is true while the job is still going to happen or is happening.
func (j Task) Live() bool { return j.State == Queued || j.State == Running }

// Report is how a handler says how far it has got. Cheap to call: writes are
// throttled here rather than being the handler's problem.
type Report func(progress float64, detail string)

// Handler does the work of one kind of job.
//
// It is given a context that is cancelled when the person cancels the job or
// the daemon is shutting down, and is expected to stop when that happens. An
// error is the job's failure and its message is shown; nil is success.
type Handler func(ctx context.Context, j Task, report Report) error

// Queue is the worker and the table behind it.
type Queue struct {
	st  *store.Store
	bus *notify.Bus
	log *slog.Logger

	mu       sync.Mutex
	handlers map[string]Handler
	stopping map[string]context.CancelFunc

	wake chan struct{}
	now  func() time.Time
}

func New(st *store.Store, bus *notify.Bus, log *slog.Logger) *Queue {
	if log == nil {
		log = slog.Default()
	}
	return &Queue{
		st:       st,
		bus:      bus,
		log:      log,
		handlers: map[string]Handler{},
		stopping: map[string]context.CancelFunc{},
		wake:     make(chan struct{}, 1),
		now:      time.Now,
	}
}

// Register says what to do with a kind of job. Kinds are namespaced by the
// feature that owns them — "media.transcode" — and the part before the dot is
// the category its failure is reported under.
func (q *Queue) Register(kind string, h Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = h
}

// stamp is a timestamp that sorts as a string in the order it happened.
//
// RFC3339Nano trims trailing zeros, so ".5Z" sorts after ".50001Z" and a whole
// second sorts after everything inside it — and the queue's order is `ORDER BY
// created_at`. Nine digits, always.
const stamp = "2006-01-02T15:04:05.000000000Z"

const cols = `id, kind, payload, state, progress, detail, error, subject, label, actor, created_at, started_at, finished_at`

func scanJob(row interface{ Scan(...any) error }) (Task, error) {
	var j Task
	err := row.Scan(&j.ID, &j.Kind, &j.Payload, &j.State, &j.Progress, &j.Detail, &j.Error,
		&j.Subject, &j.Label, &j.Actor, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	return j, err
}

// Add puts work on the queue and returns it as it was written down.
func (q *Queue) Add(ctx context.Context, actor, kind, subject, label string, payload any) (Task, error) {
	body := "{}"
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Task{}, err
		}
		body = string(b)
	}
	j := Task{
		ID:        newID(),
		Kind:      kind,
		Payload:   body,
		State:     Queued,
		Subject:   subject,
		Label:     label,
		Actor:     actor,
		CreatedAt: q.now().UTC().Format(stamp),
	}
	if _, err := q.st.DB.ExecContext(ctx,
		`INSERT INTO work (id, server_id, kind, payload, state, subject, label, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, q.st.ServerID, j.Kind, j.Payload, j.State, j.Subject, j.Label, j.Actor, j.CreatedAt); err != nil {
		return Task{}, err
	}
	q.nudge()
	return j, nil
}

// List is what is going on, newest first. An empty subject is everything.
func (q *Queue) List(ctx context.Context, subject string, limit int) ([]Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{q.st.ServerID}
	where := "server_id = ?"
	if subject != "" {
		where += " AND subject = ?"
		args = append(args, subject)
	}
	args = append(args, limit)
	rows, err := q.st.DB.QueryContext(ctx,
		`SELECT `+cols+` FROM work WHERE `+where+` ORDER BY created_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Task{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Get is one job.
func (q *Queue) Get(ctx context.Context, id string) (*Task, error) {
	j, err := scanJob(q.st.DB.QueryRowContext(ctx,
		`SELECT `+cols+` FROM work WHERE id = ? AND server_id = ?`, id, q.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoTask
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Cancel stops a job, whether it has started or not.
//
// A queued job simply never runs. A running one has its context cancelled,
// which is the handler's cue to stop; the row is marked here rather than when
// the handler returns, so the panel says "cancelled" the moment it is asked
// rather than whenever ffmpeg gets around to noticing.
func (q *Queue) Cancel(ctx context.Context, actor, id string) error {
	res, err := q.st.DB.ExecContext(ctx,
		`UPDATE work SET state = ?, finished_at = ?, detail = '' WHERE id = ? AND server_id = ? AND state IN (?, ?)`,
		Cancelled, q.now().UTC().Format(stamp), id, q.st.ServerID, Queued, Running)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either it is not here, or it already ended. Neither is an error worth
		// a banner: the button is gone by the time the answer arrives.
		if _, err := q.Get(ctx, id); err != nil {
			return err
		}
		return nil
	}
	q.mu.Lock()
	stop := q.stopping[id]
	q.mu.Unlock()
	if stop != nil {
		stop()
	}
	_ = q.st.Audit(ctx, actor, "work.cancel", id, "")
	return nil
}

// Start recovers anything the last daemon left behind and then runs the worker
// until the context ends.
func (q *Queue) Start(ctx context.Context) error {
	if err := q.recover(ctx); err != nil {
		return err
	}
	go q.loop(ctx)
	return nil
}

// recover deals with jobs that were running when the daemon stopped.
//
// They are failed, not resumed and not re-queued. ffmpeg does not continue
// where it left off, so "running" after a restart describes nothing that is
// happening — and re-queueing on the person's behalf would start minutes of
// work on a box that has just come back up, without anybody asking.
func (q *Queue) recover(ctx context.Context) error {
	res, err := q.st.DB.ExecContext(ctx,
		`UPDATE work SET state = ?, error = ?, finished_at = ? WHERE server_id = ? AND state = ?`,
		Failed, "the daemon restarted while this was running", q.now().UTC().Format(stamp),
		q.st.ServerID, Running)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		q.log.Info("work: failed work left behind by a restart", "jobs", n)
	}
	return nil
}

// sweep keeps the table from being a log. A week is long enough to ask what
// happened yesterday and short enough that nobody has to think about it.
func (q *Queue) sweep(ctx context.Context) {
	cutoff := q.now().UTC().Add(-7 * 24 * time.Hour).Format(stamp)
	if _, err := q.st.DB.ExecContext(ctx,
		`DELETE FROM work WHERE server_id = ? AND state IN (?, ?, ?) AND created_at < ?`,
		q.st.ServerID, Done, Failed, Cancelled, cutoff); err != nil {
		q.log.Warn("work: could not sweep finished work", "err", err)
	}
}

func (q *Queue) nudge() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) loop(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	clean := time.NewTicker(6 * time.Hour)
	defer clean.Stop()
	q.sweep(ctx)
	for {
		// Drain what is waiting, one at a time, before going back to sleep.
		for {
			ran, err := q.step(ctx)
			if err != nil {
				q.log.Warn("work: could not take the next job", "err", err)
				break
			}
			if !ran || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-tick.C:
		case <-clean.C:
			q.sweep(ctx)
		}
	}
}

// step runs the oldest queued job, and reports whether there was one.
func (q *Queue) step(ctx context.Context) (bool, error) {
	j, err := q.claim(ctx)
	if err != nil || j == nil {
		return false, err
	}

	q.mu.Lock()
	h := q.handlers[j.Kind]
	q.mu.Unlock()
	if h == nil {
		// A row whose kind nothing handles is failed on sight. Left queued it
		// would be retried forever by a daemon that no longer knows how — which
		// is what happens to a job whose feature was removed.
		q.finish(ctx, *j, fmt.Errorf("nothing on this server knows how to run %q", j.Kind))
		return true, nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	q.mu.Lock()
	q.stopping[j.ID] = cancel
	q.mu.Unlock()
	// Somebody can press Cancel between the claim above and the line above
	// this: the row goes to cancelled, and there was no cancel function to call
	// yet, so the work would run to the end under a row that says it did not.
	// The window is microseconds and the outcome is a person watching a task
	// they stopped carry on, so it is closed by asking once more now that the
	// function is there to be found.
	if cur, cerr := q.Get(ctx, j.ID); cerr == nil && cur.State == Cancelled {
		cancel()
	}
	err = h(runCtx, *j, q.reporter(runCtx, j.ID))
	cancel()
	q.mu.Lock()
	delete(q.stopping, j.ID)
	q.mu.Unlock()

	q.finish(ctx, *j, err)
	return true, nil
}

// claim takes the oldest queued job. One worker makes this uncontended, but it
// is still a transaction: a claim that is not atomic is a job that runs twice
// the first time there are two of anything.
func (q *Queue) claim(ctx context.Context) (*Task, error) {
	tx, err := q.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	j, err := scanJob(tx.QueryRowContext(ctx,
		`SELECT `+cols+` FROM work WHERE server_id = ? AND state = ? ORDER BY created_at LIMIT 1`,
		q.st.ServerID, Queued))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := q.now().UTC().Format(stamp)
	res, err := tx.ExecContext(ctx,
		`UPDATE work SET state = ?, started_at = ?, progress = 0, detail = '', error = '' WHERE id = ? AND state = ?`,
		Running, now, j.ID, Queued)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	j.State, j.StartedAt = Running, now
	return &j, nil
}

// reporter writes progress, at most once a second.
//
// A transcode reports several times a second and none of those numbers is worth
// a write; what is worth a write is the last one, which the next report or the
// ending will carry anyway.
func (q *Queue) reporter(ctx context.Context, id string) Report {
	var mu sync.Mutex
	var last time.Time
	var lastDetail string
	return func(progress float64, detail string) {
		if progress < 0 {
			progress = 0
		}
		if progress > 1 {
			progress = 1
		}
		mu.Lock()
		soon := time.Since(last) < time.Second && detail == lastDetail
		if !soon {
			last = time.Now()
			lastDetail = detail
		}
		mu.Unlock()
		if soon {
			return
		}
		if _, err := q.st.DB.ExecContext(context.WithoutCancel(ctx),
			`UPDATE work SET progress = ?, detail = ? WHERE id = ? AND server_id = ? AND state = ?`,
			progress, detail, id, q.st.ServerID, Running); err != nil {
			q.log.Warn("work: could not record progress", "job", id, "err", err)
		}
	}
}

// finish writes the ending, unless the person got there first: a cancelled job
// is already finished, and the handler returning afterwards must not overwrite
// that with "failed: context canceled".
func (q *Queue) finish(ctx context.Context, j Task, err error) {
	done := context.WithoutCancel(ctx)
	now := q.now().UTC().Format(stamp)
	state, msg, progress := Done, "", 1.0
	if err != nil {
		state, msg, progress = Failed, err.Error(), 0
	}
	res, uerr := q.st.DB.ExecContext(done,
		`UPDATE work SET state = ?, error = ?, finished_at = ?, progress = CASE WHEN ? > 0 THEN ? ELSE progress END, detail = ''
		 WHERE id = ? AND server_id = ? AND state = ?`,
		state, msg, now, progress, progress, j.ID, q.st.ServerID, Running)
	if uerr != nil {
		q.log.Warn("work: could not record the ending", "job", j.ID, "err", uerr)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // cancelled while it ran; that ending stands
	}
	if err == nil || q.bus == nil {
		// Work that finished is visible where it was asked for. Work that did
		// not has to find the person, which is what the bus is for.
		return
	}
	q.bus.Emit(done, notify.Event{
		Category: category(j.Kind),
		Severity: notify.Warning,
		Title:    j.Label + " failed",
		Message:  msg,
		Subject:  j.Subject,
	})
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// category is the part of a kind before the dot: "media.transcode" is reported
// as a media problem, because that is the page somebody would go and look at.
func category(kind string) string {
	if i := strings.Index(kind, "."); i > 0 {
		return kind[:i]
	}
	return "system"
}
