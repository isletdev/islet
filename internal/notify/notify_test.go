package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

func newBus(t *testing.T) *Bus {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	keys, err := auth.LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, keys, discardLogger())
}

func TestRoutingCooldownAndDelivery(t *testing.T) {
	ctx := context.Background()
	b := newBus(t)
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		if r.Header.Get("X-Islet-Signature") == "" {
			t.Error("missing signature")
		}
		got = append(got, m)
	}))
	defer srv.Close()

	ch, err := b.SaveChannel(ctx, &Channel{Type: "webhook", Name: "hook", Config: map[string]string{"url": srv.URL, "secret": "s3"}, Categories: "system,container", MinSeverity: Warning, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Config["secret"] != "s3" {
		t.Fatal("config did not round-trip through encryption")
	}
	b.Emit(ctx, Event{Category: "system", Severity: Info, Title: "ignored: below min severity"})
	b.Emit(ctx, Event{Category: "deploy", Severity: Critical, Title: "ignored: category not routed"})
	b.Emit(ctx, Event{Category: "container", Severity: Warning, Title: "Container crashed", Message: "web-1 exited 137"})
	b.Emit(ctx, Event{Category: "container", Severity: Warning, Title: "Container crashed", Message: "duplicate within cooldown"})
	b.Emit(ctx, Event{Category: "container", Severity: Info, Title: "Container crashed", Message: "recovery is not suppressed but is below min severity"})
	b.deliverPending(ctx)
	if len(got) != 1 || got[0]["title"] != "Container crashed" || got[0]["message"] != "web-1 exited 137" {
		t.Fatalf("delivered = %+v", got)
	}
	evs, _ := b.Events(ctx, 10, 0)
	if len(evs) != 4 { // info, critical, warning, recovery info; the duplicate was suppressed
		t.Fatalf("events stored = %d", len(evs))
	}
	if !inQuiet(time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC), "22:00", "07:00") || inQuiet(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), "22:00", "07:00") {
		t.Fatal("overnight quiet window wrong")
	}
	if err := ValidateConfig("telegram", map[string]string{"token": "x"}); err == nil {
		t.Fatal("missing chatId accepted")
	}
}

func TestRetryOnFailure(t *testing.T) {
	ctx := context.Background()
	b := newBus(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	if _, err := b.SaveChannel(ctx, &Channel{Type: "webhook", Name: "bad", Config: map[string]string{"url": srv.URL}, Categories: "*", MinSeverity: Info, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	b.Emit(ctx, Event{Category: "system", Severity: Critical, Title: "boom"})
	b.deliverPending(ctx)
	evs, _ := b.Events(ctx, 1, 0)
	ds, _ := b.Deliveries(ctx, evs[0].ID)
	if len(ds) != 1 || ds[0].Status != "pending" || ds[0].Attempts != 1 || ds[0].Error == "" {
		t.Fatalf("delivery after failure = %+v", ds)
	}
}
