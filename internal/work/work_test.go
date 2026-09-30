package work

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

func testQueue(t *testing.T) (*Queue, *notify.Bus) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bus := notify.New(st, nil, nil)
	return New(st, bus, nil), bus
}

// waitFor is how these tests watch a worker without sleeping for a fixed time:
// the queue is a goroutine, and a test that sleeps long enough is a test that
// either wastes a second or fails on a loaded machine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func state(t *testing.T, q *Queue, id string) Task {
	t.Helper()
	j, err := q.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return *j
}

// The whole point, end to end: work is written down, picked up, reported on
// while it runs, and ends.
func TestWorkIsPickedUpReportedOnAndFinished(t *testing.T) {
	q, _ := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 1)
	q.Register("test.work", func(ctx context.Context, j Task, report Report) error {
		var args struct{ File string }
		if err := j.Args(&args); err != nil {
			return err
		}
		report(0.5, "halfway")
		seen <- args.File
		return nil
	})
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}

	j, err := q.Add(ctx, "alice", "test.work", "media:object:1", "Transcoding holiday.mov", map[string]string{"File": "holiday.mov"})
	if err != nil {
		t.Fatal(err)
	}
	if j.State != Queued {
		t.Errorf("a new job is %q", j.State)
	}
	select {
	case got := <-seen:
		if got != "holiday.mov" {
			t.Errorf("the handler was given %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never ran")
	}
	waitFor(t, "the job to finish", func() bool { return state(t, q, j.ID).State == Done })
	done := state(t, q, j.ID)
	if done.Progress != 1 {
		t.Errorf("a finished job is at %v", done.Progress)
	}
	if done.FinishedAt == "" || done.StartedAt == "" {
		t.Error("a finished job has no timestamps")
	}
	if done.Detail != "" {
		t.Errorf("a finished job still says what it is doing: %q", done.Detail)
	}
}

// A failure has to say what failed, where somebody will see it. The row is for
// the page; the event is for the person who is not looking at the page.
func TestAFailureIsRecordedAndReported(t *testing.T) {
	q, bus := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.Register("media.transcode", func(ctx context.Context, j Task, report Report) error {
		return errors.New("the file has no video stream")
	})
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := q.Add(ctx, "alice", "media.transcode", "media:object:7", "Transcoding clip.mov", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to fail", func() bool { return state(t, q, j.ID).State == Failed })
	if got := state(t, q, j.ID).Error; got != "the file has no video stream" {
		t.Errorf("the failure says %q", got)
	}

	waitFor(t, "the event", func() bool {
		evs, err := bus.Events(ctx, 10, 0)
		return err == nil && len(evs) > 0
	})
	evs, err := bus.Events(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := evs[0]
	if e.Category != "media" {
		t.Errorf("a media job failed under category %q, so nobody routing media sees it", e.Category)
	}
	if e.Severity != notify.Warning {
		t.Errorf("severity %q", e.Severity)
	}
	if !strings.Contains(e.Title, "Transcoding clip.mov") || !strings.Contains(e.Message, "no video stream") {
		t.Errorf("the event does not say what failed: %q / %q", e.Title, e.Message)
	}
	if e.Subject != "media:object:7" {
		t.Errorf("the event is not about anything: %q", e.Subject)
	}
}

// Work that finished is visible on the page that asked for it. An event for
// every finished job is how a notification channel becomes something people
// mute.
func TestSuccessIsNotAnEvent(t *testing.T) {
	q, bus := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Register("media.transcode", func(ctx context.Context, j Task, report Report) error { return nil })
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := q.Add(ctx, "alice", "media.transcode", "", "Transcoding", nil)
	waitFor(t, "the job to finish", func() bool { return state(t, q, j.ID).State == Done })
	evs, err := bus.Events(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Errorf("a job that worked sent %d notification(s)", len(evs))
	}
}

// A kind nothing handles is a job that would otherwise sit queued forever,
// retried by every daemon that starts, which is what happens to work whose
// feature was removed under it.
func TestAKindNothingHandlesFailsOnSight(t *testing.T) {
	q, _ := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := q.Add(ctx, "alice", "gone.away", "", "Something from an older version", nil)
	waitFor(t, "the job to fail", func() bool { return state(t, q, j.ID).State == Failed })
	if got := state(t, q, j.ID).Error; !strings.Contains(got, "gone.away") {
		t.Errorf("the failure does not name the kind: %q", got)
	}
}

// Running is a claim about right now. After a restart it is a claim about a
// process that no longer exists, and pretending otherwise leaves a progress bar
// that never moves again.
func TestWorkLeftRunningByARestartIsFailedWithTheReason(t *testing.T) {
	q, _ := testQueue(t)
	ctx := context.Background()
	j, err := q.Add(ctx, "alice", "media.transcode", "", "Transcoding", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.st.DB.ExecContext(ctx, `UPDATE work SET state = ? WHERE id = ?`, Running, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.recover(ctx); err != nil {
		t.Fatal(err)
	}
	got := state(t, q, j.ID)
	if got.State != Failed {
		t.Errorf("state after a restart is %q", got.State)
	}
	if !strings.Contains(got.Error, "restarted") {
		t.Errorf("the reason does not mention the restart: %q", got.Error)
	}
}

// Cancelling has to reach the work, not just the row — and the ending the
// person asked for must not be overwritten by the handler noticing afterwards.
func TestCancellingAJobStopsItAndStaysCancelled(t *testing.T) {
	q, _ := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	stopped := make(chan struct{})
	q.Register("test.slow", func(ctx context.Context, j Task, report Report) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := q.Add(ctx, "alice", "test.slow", "", "Slow thing", nil)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}
	if err := q.Cancel(ctx, "alice", j.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling did not reach the running handler")
	}
	waitFor(t, "the row to settle", func() bool { return !state(t, q, j.ID).Live() })
	if got := state(t, q, j.ID); got.State != Cancelled {
		t.Errorf("a cancelled job ended as %q (%s)", got.State, got.Error)
	}
}

// A queued job that is cancelled is simply never run.
func TestCancellingBeforeItStartsMeansItNeverRuns(t *testing.T) {
	q, _ := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ran bool
	var mu sync.Mutex
	q.Register("test.work", func(ctx context.Context, j Task, report Report) error {
		mu.Lock()
		ran = true
		mu.Unlock()
		return nil
	})
	j, _ := q.Add(context.Background(), "alice", "test.work", "", "Work", nil)
	if err := q.Cancel(context.Background(), "alice", j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if ran {
		t.Error("a cancelled job ran anyway")
	}
	if got := state(t, q, j.ID).State; got != Cancelled {
		t.Errorf("state is %q", got)
	}
}

// One at a time is the whole reason this exists rather than a goroutine per
// upload: two transcodes on a 1 vCPU box is an unreachable server.
func TestOnlyOneJobRunsAtATime(t *testing.T) {
	q, _ := testQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	now, most := 0, 0
	release := make(chan struct{})
	q.Register("test.work", func(ctx context.Context, j Task, report Report) error {
		mu.Lock()
		now++
		if now > most {
			most = now
		}
		mu.Unlock()
		<-release
		mu.Lock()
		now--
		mu.Unlock()
		return nil
	})
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		j, err := q.Add(ctx, "alice", "test.work", "", "Work", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	time.Sleep(300 * time.Millisecond)
	close(release)
	waitFor(t, "all three to finish", func() bool {
		for _, id := range ids {
			if state(t, q, id).State != Done {
				return false
			}
		}
		return true
	})
	mu.Lock()
	defer mu.Unlock()
	if most != 1 {
		t.Errorf("%d jobs ran at once", most)
	}
}

// The table is a queue, not a log.
func TestFinishedWorkIsSweptAndLiveWorkIsNot(t *testing.T) {
	q, _ := testQueue(t)
	ctx := context.Background()
	old, _ := q.Add(ctx, "alice", "test.work", "", "Old", nil)
	live, _ := q.Add(ctx, "alice", "test.work", "", "Waiting", nil)
	long := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := q.st.DB.ExecContext(ctx, `UPDATE work SET state = ?, created_at = ? WHERE id = ?`, Done, long, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.st.DB.ExecContext(ctx, `UPDATE work SET created_at = ? WHERE id = ?`, long, live.ID); err != nil {
		t.Fatal(err)
	}
	q.sweep(ctx)
	if _, err := q.Get(ctx, old.ID); !errors.Is(err, ErrNoTask) {
		t.Error("a month-old finished job is still here")
	}
	if _, err := q.Get(ctx, live.ID); err != nil {
		t.Error("a job that never ran was swept away while it was still waiting")
	}
}
