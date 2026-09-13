package sqlclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecuteReportsEachStatementAsItFinishes(t *testing.T) {
	d := newFakeDriver().
		on("SELECT 1", fakeResult{cols: []Column{{Name: "a", Class: ClassNumber}}, rows: rowsOf(1)}).
		on("SELECT 2", fakeResult{cols: []Column{{Name: "a", Class: ClassNumber}}, rows: rowsOf(2)})
	m, tgt, _ := testTarget(t, "fake-stream", d)

	var got []StatementResult
	sum, err := m.Execute(context.Background(), "run1", tgt, nil, "SELECT 1;SELECT 2", RunOptions{}, collect(&got))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].RowCount != 1 || got[1].RowCount != 2 {
		t.Errorf("row counts = %d, %d; want 1, 2", got[0].RowCount, got[1].RowCount)
	}
	if got[0].Start != 0 || got[0].End != 8 {
		t.Errorf("first statement range = [%d,%d), want [0,8): the editor highlights with these",
			got[0].Start, got[0].End)
	}
	if sum.Statements != 2 || sum.Failed != 0 {
		t.Errorf("summary = %+v, want 2 statements and no failures", sum)
	}
}

func TestRowCapTruncatesWithoutBuffering(t *testing.T) {
	d := newFakeDriver().on("SELECT * FROM big", fakeResult{
		cols: []Column{{Name: "a", Class: ClassNumber}},
		rows: rowsOf(5_000),
	})
	m, tgt, _ := testTarget(t, "fake-cap", d)

	var got []StatementResult
	if _, err := m.Execute(context.Background(), "r", tgt, nil, "SELECT * FROM big", RunOptions{RowCap: 10}, collect(&got)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if n := len(got[0].Rows); n != 10 {
		t.Fatalf("kept %d rows, want the cap of 10", n)
	}
	if !got[0].Truncated {
		t.Error("truncated flag not set: the user has no way to know there was more")
	}
}

func TestRowCapIsNeverUnbounded(t *testing.T) {
	if ClampRowCap(0) != DefaultRowCap {
		t.Errorf("zero should mean the default, got %d", ClampRowCap(0))
	}
	if got := ClampRowCap(1 << 30); got != MaxRowCap {
		t.Errorf("a huge cap should clamp to %d, got %d", MaxRowCap, got)
	}
	if got := ClampRowCap(-1); got != DefaultRowCap {
		t.Errorf("a negative cap should mean the default, got %d", got)
	}
	if got := ClampTimeout(time.Hour); got != MaxTimeout {
		t.Errorf("timeout should clamp to %s, got %s", MaxTimeout, got)
	}
}

func TestBatchStopsAtTheFirstError(t *testing.T) {
	boom := errors.New("relation \"nope\" does not exist")
	d := newFakeDriver().
		on("SELECT 1", fakeResult{cols: []Column{{Name: "a"}}, rows: rowsOf(1)}).
		on("SELECT nope", fakeResult{err: boom}).
		on("SELECT 3", fakeResult{cols: []Column{{Name: "a"}}, rows: rowsOf(1)})
	m, tgt, _ := testTarget(t, "fake-stop", d)

	var got []StatementResult
	if _, err := m.Execute(context.Background(), "r", tgt, nil, "SELECT 1;SELECT nope;SELECT 3", RunOptions{}, collect(&got)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: the third statement must not run", len(got))
	}
	if got[1].Error == nil || !strings.Contains(got[1].Error.Message, "does not exist") {
		t.Errorf("second result should carry the database's own words, got %+v", got[1].Error)
	}
}

func TestContinueOnErrorRunsTheRest(t *testing.T) {
	d := newFakeDriver().
		on("SELECT nope", fakeResult{err: errors.New("boom")}).
		on("SELECT 3", fakeResult{cols: []Column{{Name: "a"}}, rows: rowsOf(1)})
	m, tgt, _ := testTarget(t, "fake-continue", d)

	var got []StatementResult
	if _, err := m.Execute(context.Background(), "r", tgt, nil, "SELECT nope;SELECT 3", RunOptions{ContinueOnError: true}, collect(&got)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
}

func TestReadOnlyRefusesAWriteInTheDaemon(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-ro", d)
	tgt.ReadOnly = true

	var got []StatementResult
	_, err := m.Execute(context.Background(), "r", tgt, nil, "UPDATE users SET a = 1 WHERE id = 2", RunOptions{Confirmed: true}, collect(&got))
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("error = %v, want ErrReadOnly", err)
	}
	if len(got) != 0 {
		t.Error("a refused batch must not emit results")
	}
	if opened, _ := d.counts(); opened != 0 {
		t.Errorf("refused before running, so nothing should have connected; opened %d", opened)
	}
}

func TestReadOnlyStillAllowsReads(t *testing.T) {
	d := newFakeDriver().on("SELECT 1", fakeResult{cols: []Column{{Name: "a"}}, rows: rowsOf(1)})
	m, tgt, _ := testTarget(t, "fake-ro2", d)
	tgt.ReadOnly = true

	var got []StatementResult
	if _, err := m.Execute(context.Background(), "r", tgt, nil, "SELECT 1", RunOptions{}, collect(&got)); err != nil {
		t.Fatalf("a read on a read-only connection should work: %v", err)
	}
}

func TestDangerousStatementNeedsConfirmation(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-danger", d)

	_, err := m.Execute(context.Background(), "r", tgt, nil, "DELETE FROM users", RunOptions{}, collect(new([]StatementResult)))
	if !errors.Is(err, ErrNeedsConfirmation) {
		t.Fatalf("error = %v, want ErrNeedsConfirmation", err)
	}
	if !strings.Contains(err.Error(), "DELETE FROM users") {
		t.Errorf("the refusal should name what was refused, got %q", err)
	}
}

func TestABatchIsRefusedWholeOrNotAtAll(t *testing.T) {
	d := newFakeDriver().on("SELECT 1", fakeResult{cols: []Column{{Name: "a"}}, rows: rowsOf(1)})
	m, tgt, _ := testTarget(t, "fake-whole", d)

	var got []StatementResult
	_, err := m.Execute(context.Background(), "r", tgt, nil, "SELECT 1;DROP TABLE users", RunOptions{}, collect(&got))
	if !errors.Is(err, ErrNeedsConfirmation) {
		t.Fatalf("error = %v, want ErrNeedsConfirmation", err)
	}
	if len(got) != 0 {
		t.Fatal("the safe first statement must not run either: half an applied batch is the worst outcome")
	}
}

func TestCancelReachesTheServer(t *testing.T) {
	d := newFakeDriver().on("SELECT pg_sleep(60)", fakeResult{block: true})
	m, tgt, _ := testTarget(t, "fake-cancel", d)

	done := make(chan StatementResult, 1)
	go func() {
		var got []StatementResult
		_, _ = m.Execute(context.Background(), "run-42", tgt, nil, "SELECT pg_sleep(60)", RunOptions{}, collect(&got))
		if len(got) > 0 {
			done <- got[0]
		} else {
			done <- StatementResult{}
		}
	}()

	// Wait for the statement to be registered, then cancel it by id.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := m.Cancel(context.Background(), "run-42", "alice"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never registered itself for cancellation")
		}
		time.Sleep(time.Millisecond)
	}

	select {
	case res := <-done:
		if res.Error == nil || res.Error.Code != "cancelled" {
			t.Fatalf("result = %+v, want a cancelled error", res.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop the statement")
	}

	d.mu.Lock()
	conns := append([]*fakeConn(nil), d.conns...)
	d.mu.Unlock()
	total := 0
	for _, c := range conns {
		total += c.cancelCount()
	}
	if total == 0 {
		t.Error("cancel never reached the connection: abandoning the response is not a cancel")
	}
}

func TestCancelBelongsToItsOwner(t *testing.T) {
	d := newFakeDriver()
	m, _, _ := testTarget(t, "fake-owner", d)
	if err := m.Cancel(context.Background(), "nope", "mallory"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelling an unknown run = %v, want ErrNotFound", err)
	}
}

func TestTransactionModeNeedsASession(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-txns", d)
	_, err := m.Execute(context.Background(), "r", tgt, nil, "SELECT 1", RunOptions{Transaction: true}, collect(new([]StatementResult)))
	if err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("error = %v, want an explanation that transaction mode needs a session", err)
	}
}

func TestSessionKeepsItsOwnConnectionAndTransactionState(t *testing.T) {
	d := newFakeDriver().
		on("BEGIN", fakeResult{}).
		on("UPDATE t SET a = 1 WHERE id = 2", fakeResult{affected: 1}).
		on("COMMIT", fakeResult{})
	m, tgt, _ := testTarget(t, "fake-sess", d)

	sess, err := m.OpenSession(context.Background(), tgt.Ref, tgt.User, tgt.Config)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	opt := RunOptions{Transaction: true, Confirmed: true}
	if _, err := m.Execute(context.Background(), "r1", tgt, sess, "UPDATE t SET a = 1 WHERE id = 2", opt, collect(new([]StatementResult))); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !sess.InTransaction() {
		t.Fatal("transaction mode should leave the transaction open for the user to commit")
	}
	if _, err := m.Execute(context.Background(), "r2", tgt, sess, "COMMIT", RunOptions{}, collect(new([]StatementResult))); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if sess.InTransaction() {
		t.Error("after COMMIT the session should no longer report an open transaction")
	}
	if opened, _ := d.counts(); opened != 1 {
		t.Errorf("a session is one connection, opened %d", opened)
	}
}

func TestSessionBelongsToItsUser(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-sessowner", d)
	sess, err := m.OpenSession(context.Background(), tgt.Ref, "alice", tgt.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.LookupSession(sess.ID, "mallory"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user found the session: %v", err)
	}
	if _, err := m.LookupSession(sess.ID, "alice"); err != nil {
		t.Fatalf("the owner could not find their own session: %v", err)
	}
}

func TestAbandonedSessionIsRolledBackAndReleased(t *testing.T) {
	d := newFakeDriver().on("BEGIN", fakeResult{}).on("SELECT 1", fakeResult{cols: []Column{{Name: "a"}}, rows: rowsOf(1)})
	m, tgt, clk := testTarget(t, "fake-abandon", d)

	sess, err := m.OpenSession(context.Background(), tgt.Ref, tgt.User, tgt.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(context.Background(), "r", tgt, sess, "SELECT 1", RunOptions{Transaction: true}, collect(new([]StatementResult))); err != nil {
		t.Fatal(err)
	}

	clk.advance(SessionIdleTimeout + time.Minute)
	m.Sweep()

	if _, err := m.LookupSession(sess.ID, tgt.User); !errors.Is(err, ErrNotFound) {
		t.Error("an idle session should be gone after the janitor runs")
	}
	d.mu.Lock()
	conn := d.conns[0]
	d.mu.Unlock()
	if !strings.Contains(joined(conn.statements()), "ROLLBACK") {
		t.Errorf("an abandoned open transaction must be rolled back, ran: %s", joined(conn.statements()))
	}
}

func TestUnknownEngineSaysSoInsteadOfHanging(t *testing.T) {
	m := NewManager(nil, nil)
	_, err := m.Execute(context.Background(), "r", Target{Engine: "mongo", Config: Config{Engine: "mongo"}}, nil, "SELECT 1", RunOptions{}, collect(new([]StatementResult)))
	if err == nil || !strings.Contains(err.Error(), "mongo") {
		t.Fatalf("error = %v, want one that names the engine and points somewhere useful", err)
	}
}

// A statement the server killed comes back through pgx as a cursor with no
// columns whose error appears only once it is asked for a row. Skipping the
// row loop for a statement that returned no columns turned that into a silent
// success: no error, no rows, and a run that reported nothing wrong.
func TestAFailureReportedOnlyByTheCursorIsNotLost(t *testing.T) {
	d := newFakeDriver().on("SELECT pg_sleep(5)", fakeResult{
		deferredErr: context.DeadlineExceeded,
	})
	m, tgt, _ := testTarget(t, "fake-deferred", d)

	var got []StatementResult
	sum, err := m.Execute(context.Background(), "run", tgt, nil, "SELECT pg_sleep(5)",
		RunOptions{Timeout: time.Second}, collect(&got))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d results, want 1", len(got))
	}
	if got[0].Error == nil {
		t.Fatal("the statement was killed and the result says nothing went wrong")
	}
	if got[0].Error.Code != "timeout" {
		t.Errorf("error code = %q, want timeout: %s", got[0].Error.Code, got[0].Error.Message)
	}
	if sum.Failed != 1 {
		t.Errorf("summary says %d failed, want 1", sum.Failed)
	}
}
