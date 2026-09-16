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

// A deleted account leaves no rule behind. Nothing would let it in — there is
// nobody to sign in as — but usernames are reusable, so a new account with an
// old name would otherwise inherit whatever the old one could reach.
func TestForgetUserClearsEveryList(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m := New(nil, st, t.TempDir(), "")

	for _, d := range []*Domain{
		{Host: "a.example.com", TargetType: "url", Target: "http://a", TLS: "none", Protect: true, ProtectUsers: "alice,bob", Enabled: true,
			Locations: []Location{{Path: "/admin", TargetType: "url", Target: "http://x", Protect: "on", ProtectUsers: "bob"}}},
		{Host: "b.example.com", TargetType: "url", Target: "http://b", TLS: "none", Protect: true, ProtectUsers: "carol", Enabled: true},
	} {
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

	// The rules are rewritten before anything is reconciled, and reconciling
	// needs Docker, which a unit test does not have: the error is the proxy
	// reload, not the change being tested.
	res, err := m.ForgetUser(ctx, "admin", "Bob")
	if err != nil {
		t.Logf("reconcile (expected without docker): %v", err)
	}
	if res.Rules != 2 {
		t.Errorf("rewrote %d rules, want 2", res.Rules)
	}
	// The location named nobody else, so it now names nobody at all — still a
	// login, no longer a particular person. The caller is told so it can say so.
	if len(res.Widened) != 1 || res.Widened[0] != "a.example.com/admin" {
		t.Errorf("widened = %v, want [a.example.com/admin]", res.Widened)
	}

	doms, err := m.Domains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range doms {
		switch d.Host {
		case "a.example.com":
			if d.ProtectUsers != "alice" {
				t.Errorf("host list is %q, want alice", d.ProtectUsers)
			}
			if got := d.Locations[0].ProtectUsers; got != "" {
				t.Errorf("location list is %q, want empty", got)
			}
			// An emptied list means "any signed-in user", which is a widening
			// nobody asked for — but the path still asks for a login, and the
			// alternative is a rule that silently names a ghost.
			if d.Locations[0].Protect != "on" {
				t.Errorf("the location stopped asking for a login: %q", d.Locations[0].Protect)
			}
		case "b.example.com":
			if d.ProtectUsers != "carol" {
				t.Errorf("an unrelated list was rewritten: %q", d.ProtectUsers)
			}
		}
	}
}
