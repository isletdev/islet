package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/store"
)

// The switch sends every site to the maintenance page, locations included, and
// leaves the panel's own domain alone: turning it on must not lock out the
// person who has to turn it off.
func TestServerWideMaintenance(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m := New(nil, st, dir, "")
	site := &Domain{Host: "shop.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true,
		Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y", Protect: "inherit"}}}
	panel := &Domain{Host: "panel.example.com", TargetType: "panel", TLS: "none", Enabled: true}
	for _, d := range []*Domain{site, panel} {
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := m.writeDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := m.saveLocations(ctx, d.ID, d.Locations); err != nil {
			t.Fatal(err)
		}
	}
	cfg := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "proxy", "dynamic", "islet.yml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if _, err := m.SetMaintenance(ctx, "admin", Maintenance{On: true, Message: "Back after the migration."}); err != nil {
		t.Fatal(err)
	}
	y := cfg()
	if strings.Count(y, "/_islet/maintenance") != 1 || !strings.Contains(y, "d-"+site.ID+"-maint") {
		t.Fatalf("the site was not sent to the maintenance page:\n%s", y)
	}
	for _, r := range []string{"d-" + site.ID + ":", "d-" + site.ID + "-l0:"} {
		i := strings.Index(y, r)
		if i < 0 {
			t.Fatalf("no router %q", r)
		}
		if blk := y[i:min(i+900, len(y))]; !strings.Contains(blk, "-maint") {
			t.Errorf("router %q is not in maintenance:\n%s", r, blk)
		}
	}
	if i := strings.Index(y, "d-"+panel.ID+":"); i < 0 || strings.Contains(y[i:min(i+600, len(y))], "-maint") {
		t.Error("the panel's own domain went into maintenance")
	}
	// The domain rows are untouched: turning the switch off brings back
	// exactly what was there, including a site that was in maintenance alone.
	if got, _ := m.Domain(ctx, site.ID); got.Maintenance {
		t.Error("the switch was written into the domain")
	}
	if _, err := m.SetMaintenance(ctx, "admin", Maintenance{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg(), "/_islet/maintenance") {
		t.Error("turning it off left a site in maintenance")
	}
	if mt := m.Maintenance(ctx); mt.On || mt.ChangedBy != "admin" || mt.ChangedAt == "" {
		t.Errorf("stored switch = %+v", mt)
	}
}

func TestMaintenancePageAndValidation(t *testing.T) {
	until := time.Now().Add(90 * time.Minute).UTC().Format(time.RFC3339)
	mt := Maintenance{On: true, Message: "Database <upgrade> tonight", Until: until}
	if err := mt.Validate(); err != nil {
		t.Fatal(err)
	}
	page := string(MaintenanceHTML(mt))
	if !strings.Contains(page, "Database &lt;upgrade&gt; tonight") || !strings.Contains(page, `datetime="`+until+`"`) {
		t.Fatalf("page = %s", page)
	}
	if j := string(MaintenanceJSON(mt)); !strings.Contains(j, `"error":"maintenance"`) || !strings.Contains(j, until) {
		t.Fatalf("json = %s", j)
	}
	if ra := mt.RetryAfter(time.Now()); ra < 5300 || ra > 5401 {
		t.Errorf("Retry-After = %d, want about 5400", ra)
	}
	// A time that has passed is not promised to anybody.
	old := Maintenance{Until: "2020-01-01T00:00:00Z"}
	if strings.Contains(string(MaintenanceHTML(old)), "Expected back") || old.RetryAfter(time.Now()) != 120 {
		t.Error("a past end time was shown")
	}
	if !strings.Contains(string(MaintenanceHTML(Maintenance{})), DefaultMaintenanceMessage) {
		t.Error("no message, no default")
	}
	for _, bad := range []Maintenance{{Until: "tomorrow"}, {Message: strings.Repeat("x", 501)}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
