package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/store"
)

// Naming the first person from outside must not shut out the panel accounts
// the empty list let in, and taking the last one off must leave the list as it
// was.
func TestWithNameKeepsAnyAccount(t *testing.T) {
	cases := []struct {
		list, name string
		want       bool
		out        string
	}{
		{"", "site:bob", true, "*,site:bob"},
		{"*,site:bob", "site:bob", false, ""},
		{"alice", "site:bob", true, "alice,site:bob"},
		{"alice,site:bob", "site:bob", false, "alice"},
		{"site:bob", "site:bob", true, "site:bob"},
		{"*", "site:bob", false, ""},
	}
	for _, c := range cases {
		if got := withName(c.list, c.name, c.want); got != c.out {
			t.Errorf("withName(%q, %q, %v) = %q, want %q", c.list, c.name, c.want, got, c.out)
		}
	}
}

// A protected host serves its own sign-in, at three exact paths, outside its
// gate and above its locations. An open host serves none.
func TestRenderSignInRoute(t *testing.T) {
	doms := []Domain{
		{ID: "p", Host: "client.example.com", TargetType: "url", Target: "http://x", TLS: "letsencrypt", Enabled: true, Protect: true, ProtectUsers: "site:bob",
			Locations: []Location{{ID: "l", Path: "/a-rather-long-path-name", TargetType: "url", Target: "http://y", Protect: "inherit"}}},
		{ID: "o", Host: "open.example.com", TargetType: "url", Target: "http://x", TLS: "letsencrypt", Enabled: true},
		{ID: "a", Host: "admin.example.com", TargetType: "url", Target: "http://x", TLS: "letsencrypt", Enabled: true,
			Locations: []Location{{ID: "l", Path: "/admin", TargetType: "url", Target: "http://y", Protect: "on"}}},
	}
	out, err := Render(doms, "https://host.docker.internal:9443")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out)
	for _, want := range []string{"d-p-gate:", "d-a-gate:", "Path(`/_islet/login`) || Path(`/_islet/logout`) || Path(`/_islet/invite`)"} {
		if !strings.Contains(y, want) {
			t.Errorf("rendered config lacks %q", want)
		}
	}
	if strings.Contains(y, "d-o-gate") {
		t.Error("an open host serves a sign-in page")
	}
	gate := y[strings.Index(y, "d-p-gate:"):]
	gate = gate[:strings.Index(gate, "rule:")+200]
	if strings.Contains(gate, "-protect") {
		t.Error("the sign-in page is behind the gate it signs people in to")
	}
	if !strings.Contains(gate, "priority: 200") {
		t.Errorf("the sign-in route does not outrank the host's locations:\n%s", gate)
	}
}

// Giving one person access writes exactly the routes asked for, and the
// sign-in host lookup finds the domain an exact name or a wildcard covers.
func TestSetAccessAndLookup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m := New(nil, st, dir, "")
	a := &Domain{Host: "client.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true, Protect: true,
		Locations: []Location{{Path: "/admin", TargetType: "url", Target: "http://y", Protect: "on", ProtectUsers: "alice"}}}
	w := &Domain{Host: "*.apps.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true, Protect: true}
	open := &Domain{Host: "open.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true}
	for _, d := range []*Domain{a, w, open} {
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
	got, _ := m.Domain(ctx, a.ID)
	admin := a.ID + "/" + got.Locations[0].ID
	if err := m.SetAccess(ctx, "admin", "site:bob", map[RouteKey]bool{a.ID: true, admin: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Domain(ctx, a.ID)
	if got.ProtectUsers != "*,site:bob" || got.Locations[0].ProtectUsers != "alice,site:bob" {
		t.Fatalf("after granting: root %q, /admin %q", got.ProtectUsers, got.Locations[0].ProtectUsers)
	}
	if err := m.SetAccess(ctx, "admin", "site:bob", map[RouteKey]bool{admin: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Domain(ctx, a.ID)
	if got.ProtectUsers != "" || got.Locations[0].ProtectUsers != "alice,site:bob" {
		t.Fatalf("after taking the root away: root %q, /admin %q", got.ProtectUsers, got.Locations[0].ProtectUsers)
	}
	if cfg, err := os.ReadFile(filepath.Join(dir, "proxy", "dynamic", "islet.yml")); err != nil || !strings.Contains(string(cfg), "site%3Abob") {
		t.Fatalf("the change was not applied to the proxy: %v", err)
	}

	for host, want := range map[string]string{"client.example.com": a.ID, "x.apps.example.com": w.ID, "a.b.apps.example.com": "", "open.example.com": "", "apps.example.com": ""} {
		d := m.ProtectedDomainFor(ctx, host)
		if (d == nil && want != "") || (d != nil && d.ID != want) {
			t.Errorf("ProtectedDomainFor(%q) = %v, want %q", host, d, want)
		}
	}
}
