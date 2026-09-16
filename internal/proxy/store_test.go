package proxy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/isletdev/islet/internal/store"
)

// A domain has to come back out of the database as it went in.
//
// This exists because it did not: protect_users was added to the select, the
// scan and the insert's column list, and the insert's placeholders were left
// one short. Nothing in the render tests could see it — the fault was between
// the form and the row, and every test that mattered started from a struct.
func TestDomainRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m := New(nil, st, t.TempDir(), "")

	in := &Domain{
		Host: "app.example.com", TargetType: "url", Target: "http://127.0.0.1:9999", TLS: "none",
		Protect: true, ProtectUsers: "alice,bob", RedirectWWW: true, Maintenance: true,
		BlockExploits: true, PassHost: true, Enabled: true, RateLimit: 5,
		IPAllowlist: "10.0.0.0/8", Headers: "X-A: b", PathPrefix: "/p",
		Locations: []Location{
			{Path: "/hooks", TargetType: "url", Target: "http://h", Protect: "off"},
			{Path: "/admin", TargetType: "url", Target: "http://a", Protect: "on", ProtectUsers: "alice", StripPath: true},
			{Path: "/api", TargetType: "url", Target: "http://i"},
		},
	}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := m.writeDomain(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := m.saveLocations(ctx, in.ID, in.Locations); err != nil {
		t.Fatal(err)
	}
	out, err := m.Domain(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Protect != in.Protect || out.ProtectUsers != in.ProtectUsers {
		t.Errorf("protection did not survive: %v %q", out.Protect, out.ProtectUsers)
	}
	if out.RedirectWWW != in.RedirectWWW || out.Maintenance != in.Maintenance || out.BlockExploits != in.BlockExploits ||
		out.PassHost != in.PassHost || out.Enabled != in.Enabled || out.RateLimit != in.RateLimit ||
		out.IPAllowlist != in.IPAllowlist || out.Headers != in.Headers || out.PathPrefix != in.PathPrefix {
		t.Errorf("a column was lost:\nin  %+v\nout %+v", *in, *out)
	}
	if len(out.Locations) != 3 {
		t.Fatalf("want 3 locations, got %d", len(out.Locations))
	}
	for i, want := range in.Locations {
		got := out.Locations[i]
		if got.Path != want.Path || got.Protect != want.Protect || got.ProtectUsers != want.ProtectUsers || got.StripPath != want.StripPath {
			t.Errorf("location %d came back changed:\nin  %+v\nout %+v", i, want, got)
		}
	}

	// The same domain saved again: the update path lists the columns a second
	// time, and is the half that a round trip through insert alone misses.
	out.ProtectUsers = "carol"
	out.Locations[1].ProtectUsers = ""
	if err := m.writeDomain(ctx, out); err != nil {
		t.Fatal(err)
	}
	if err := m.saveLocations(ctx, out.ID, out.Locations); err != nil {
		t.Fatal(err)
	}
	again, err := m.Domain(ctx, out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ProtectUsers != "carol" || again.Locations[1].ProtectUsers != "" || again.Locations[1].Protect != "on" {
		t.Errorf("an update was lost: %q %+v", again.ProtectUsers, again.Locations[1])
	}
}
