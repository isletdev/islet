package sqlclient

import (
	"context"
	"testing"
	"time"
)

func TestPoolReusesAConnection(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-reuse", d)

	for i := 0; i < 3; i++ {
		if err := m.Use(context.Background(), tgt.Ref, tgt.User, tgt.Config, func(Conn) error { return nil }); err != nil {
			t.Fatalf("use %d: %v", i, err)
		}
	}
	if opened, _ := d.counts(); opened != 1 {
		t.Errorf("opened %d connections for three sequential uses, want 1", opened)
	}
}

func TestPoolNeverExceedsItsLimit(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-limit", d)

	// Hold every connection the pool is allowed to hand out.
	held := make([]*Session, 0, PoolMaxConns)
	for i := 0; i < PoolMaxConns; i++ {
		s, err := m.OpenSession(context.Background(), tgt.Ref, tgt.User, tgt.Config)
		if err != nil {
			t.Fatalf("session %d: %v", i, err)
		}
		held = append(held, s)
	}
	if opened, _ := d.counts(); opened != PoolMaxConns {
		t.Fatalf("opened %d, want %d", opened, PoolMaxConns)
	}

	// The next caller waits rather than opening a fifth: a panel must not
	// exhaust a small database's connection limit.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.OpenSession(ctx, tgt.Ref, tgt.User, tgt.Config); err == nil {
		t.Fatal("a fifth connection was handed out")
	}
	if opened, _ := d.counts(); opened != PoolMaxConns {
		t.Errorf("opened %d after the refused attempt, want %d", opened, PoolMaxConns)
	}

	// Give one back and the waiter gets in.
	if err := m.CloseSession(context.Background(), held[0].ID, tgt.User); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenSession(context.Background(), tgt.Ref, tgt.User, tgt.Config); err != nil {
		t.Fatalf("after a release, a new session should open: %v", err)
	}
}

func TestPoolsAreSeparatePerUser(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-peruser", d)

	for _, user := range []string{"alice", "bob"} {
		if err := m.Use(context.Background(), tgt.Ref, user, tgt.Config, func(Conn) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if opened, _ := d.counts(); opened != 2 {
		t.Errorf("opened %d, want one pool per user", opened)
	}
}

func TestIdleConnectionsAreClosed(t *testing.T) {
	d := newFakeDriver()
	m, tgt, clk := testTarget(t, "fake-idle", d)

	if err := m.Use(context.Background(), tgt.Ref, tgt.User, tgt.Config, func(Conn) error { return nil }); err != nil {
		t.Fatal(err)
	}
	clk.advance(PoolIdleTimeout + time.Second)
	m.Sweep()

	deadline := time.Now().Add(time.Second)
	for {
		if _, closed := d.counts(); closed == 1 {
			return
		}
		if time.Now().After(deadline) {
			_, closed := d.counts()
			t.Fatalf("closed %d idle connections, want 1", closed)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUnusedPoolIsThrownAway(t *testing.T) {
	d := newFakeDriver()
	m, tgt, clk := testTarget(t, "fake-unused", d)

	if err := m.Use(context.Background(), tgt.Ref, tgt.User, tgt.Config, func(Conn) error { return nil }); err != nil {
		t.Fatal(err)
	}
	clk.advance(PoolMaxUnused + time.Minute)
	m.Sweep()

	m.mu.Lock()
	n := len(m.pools)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("%d pools left after the idle window; idle memory is the whole argument for this feature", n)
	}
}

func TestForgetDropsEverythingForAConnection(t *testing.T) {
	d := newFakeDriver()
	m, tgt, _ := testTarget(t, "fake-forget", d)

	sess, err := m.OpenSession(context.Background(), tgt.Ref, tgt.User, tgt.Config)
	if err != nil {
		t.Fatal(err)
	}
	// A recreated container gets a new bridge address, so the old pool is
	// pointing at nothing and must not be reused.
	m.Forget(tgt.Ref)

	if _, err := m.LookupSession(sess.ID, tgt.User); err == nil {
		t.Error("the session survived Forget")
	}
	m.mu.Lock()
	n := len(m.pools)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("%d pools survived Forget", n)
	}
}

func TestSessionRefusesConcurrentRuns(t *testing.T) {
	d := newFakeDriver().on("SELECT pg_sleep(60)", fakeResult{block: true})
	m, tgt, _ := testTarget(t, "fake-busy", d)

	sess, err := m.OpenSession(context.Background(), tgt.Ref, tgt.User, tgt.Config)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = m.Execute(context.Background(), "slow", tgt, sess, "SELECT pg_sleep(60)", RunOptions{}, collect(new([]StatementResult)))
	}()

	// Wait for the slow run to actually hold the session before racing it.
	// Firing the second run immediately is a coin toss over which goroutine
	// gets there first, and when this one wins the slow run is the one that is
	// refused and the session is then free forever.
	if !waitBusy(sess, 2*time.Second) {
		t.Fatal("the slow run never took the session")
	}

	if _, err := m.Execute(context.Background(), "second", tgt, sess, "SELECT 1", RunOptions{}, collect(new([]StatementResult))); err != ErrBusy {
		t.Fatalf("a second run on a busy session returned %v, want ErrBusy", err)
	}
	_ = m.Cancel(context.Background(), "slow", tgt.User)
}

// waitBusy reports whether the session became busy within d.
func waitBusy(s *Session, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if busy {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}
