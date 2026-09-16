package proxy

import (
	"strings"
	"testing"
)

// block returns the router stanza a rule belongs to, by working back from the
// rule line to the name that opened it.
// flat collapses every run of whitespace, because yaml.v3 folds a line longer
// than eighty columns and a folded plain scalar is the same string with the
// break standing in for a space.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// router returns one named router's stanza, which survives a folded rule line
// where searching for the rule itself does not.
func router(out, name string) string {
	i := strings.Index(out, "\n        "+name+":\n")
	if i < 0 {
		return ""
	}
	rest := out[i+1:]
	if j := strings.Index(rest[1:], "\n        d-"); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

func block(out string, idx int) string {
	if idx < 0 {
		return ""
	}
	start := strings.LastIndex(out[:idx], "\n        d-")
	if start < 0 {
		return ""
	}
	return out[start:idx]
}

func TestRenderProtect(t *testing.T) {
	raw, err := Render([]Domain{
		{ID: "a1", Host: "app.example.com", TargetType: "url", Target: "http://127.0.0.1:9999", TLS: "none", Protect: true, Enabled: true},
		{ID: "b2", Host: "open.example.com", TargetType: "url", Target: "http://127.0.0.1:9998", TLS: "none", Enabled: true},
	}, "http://host.docker.internal:9443")
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	i := strings.Index(out, "Host(`app.example.com`)")
	j := strings.Index(out, "Host(`open.example.com`)")
	if i < 0 || j < 0 {
		t.Fatalf("routers missing:\n%s", out)
	}
	if !strings.Contains(out, "address: http://host.docker.internal:9443/_islet/auth") {
		t.Fatalf("forwardAuth middleware missing:\n%s", out)
	}
	if !strings.Contains(block(out, i), "d-a1-protect") {
		t.Fatalf("protected router lacks forward auth:\n%s", block(out, i))
	}
	if strings.Contains(block(out, j), "-protect") {
		t.Fatalf("open router has forward auth:\n%s", block(out, j))
	}
}

// The allow-list travels in the address Traefik calls, so the daemon never has
// to work out for itself which rule a request matched.
func TestRenderProtectUsers(t *testing.T) {
	raw, err := Render([]Domain{
		{ID: "a1", Host: "app.example.com", TargetType: "url", Target: "http://x", TLS: "none", Protect: true, ProtectUsers: "alice,bob", Enabled: true},
	}, "http://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	if out := string(raw); !strings.Contains(out, "address: http://panel:9443/_islet/auth?u=alice%2Cbob") {
		t.Fatalf("allow-list is not in the auth address:\n%s", out)
	}
}

// A path may ask for a login on a site that does not, and each path may name
// people of its own.
func TestRenderProtectPerLocation(t *testing.T) {
	raw, err := Render([]Domain{
		{ID: "a", Host: "shop.example.com", TargetType: "url", Target: "http://web", TLS: "none", Enabled: true,
			Locations: []Location{
				{ID: "l1", Path: "/admin", TargetType: "url", Target: "http://admin", Protect: "on", ProtectUsers: "alice"},
				{ID: "l2", Path: "/api", TargetType: "url", Target: "http://api", Protect: "inherit"},
			}},
	}, "http://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	admin := block(out, strings.Index(out, "PathPrefix(`/admin`)"))
	api := block(out, strings.Index(out, "PathPrefix(`/api`)"))
	root := block(out, strings.Index(out, "rule: Host(`shop.example.com`)\n"))
	if !strings.Contains(admin, "d-a-l0-protect") {
		t.Fatalf("/admin is not protected:\n%s", admin)
	}
	if strings.Contains(api, "-protect") {
		t.Fatalf("/api inherited protection from an open host:\n%s", api)
	}
	if strings.Contains(root, "-protect") {
		t.Fatalf("the open root became protected:\n%s", root)
	}
	if !strings.Contains(out, "address: http://panel:9443/_islet/auth?u=alice") {
		t.Fatalf("the location's allow-list is missing:\n%s", out)
	}
}

// And the other direction: one path left open on a protected host, which is
// what a webhook receiver needs — the service calling it has no session.
func TestRenderProtectLocationOptOut(t *testing.T) {
	raw, err := Render([]Domain{
		{ID: "a", Host: "shop.example.com", TargetType: "url", Target: "http://web", TLS: "none", Protect: true, Enabled: true,
			Locations: []Location{
				{ID: "l1", Path: "/hooks", TargetType: "url", Target: "http://hooks", Protect: "off"},
				{ID: "l2", Path: "/api", TargetType: "url", Target: "http://api"},
			}},
	}, "http://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	hooks := router(out, "d-a-l0") // the /hooks location
	api := block(out, strings.Index(out, "PathPrefix(`/api`)"))
	if hooks == "" {
		t.Fatalf("no router for /hooks:\n%s", out)
	}
	if strings.Contains(hooks, "-protect") {
		t.Fatalf("/hooks is still protected:\n%s", hooks)
	}
	// And it opens that path only. PathPrefix is a string prefix in Traefik,
	// so an unbounded rule would hand /hooksecret to the same open router.
	if !strings.Contains(flat(out), "rule: Host(`shop.example.com`) && (Path(`/hooks`) || PathPrefix(`/hooks/`))") {
		t.Fatalf("the opt-out rule is not bounded to the path:\n%s", out)
	}
	if !strings.Contains(api, "d-a-protect") {
		t.Fatalf("/api did not inherit the host's protection:\n%s", api)
	}
}

func TestProtectValidate(t *testing.T) {
	d := Domain{Host: "a.example.com", TargetType: "url", Target: "http://x", ProtectUsers: " Alice, bob ,alice,",
		Locations: []Location{{Path: "/admin", TargetType: "url", Target: "http://y", Protect: "on"}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.ProtectUsers != "alice,bob" {
		t.Fatalf("allow-list not normalised: %q", d.ProtectUsers)
	}
	if d.Locations[0].Protect != "on" {
		t.Fatalf("location protect changed: %q", d.Locations[0].Protect)
	}
	// An empty value is the old meaning, not an error.
	d.Locations[0].Protect = ""
	if err := d.Validate(); err != nil || d.Locations[0].Protect != "inherit" {
		t.Fatalf("empty protect should become inherit: %v %q", err, d.Locations[0].Protect)
	}
	d.Locations[0].Protect = "maybe"
	if err := d.Validate(); err == nil {
		t.Fatal("a nonsense protect value was accepted")
	}
}

// The panel serves the login the gate redirects to. Putting the gate in front
// of the panel's own host sends an anonymous visitor to a page that triggers
// the gate again, and the browser gives up after twenty hops.
func TestRenderProtectSkipsThePanelHost(t *testing.T) {
	raw, err := Render([]Domain{
		{ID: "p", Host: "panel.example.com", TargetType: "panel", TLS: "none", Protect: true, Enabled: true},
	}, "http://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	if out := string(raw); strings.Contains(out, "-protect") {
		t.Fatalf("the panel host got a forward-auth gate in front of its own login:\n%s", out)
	}
}

// Only a rule that weakens the gate is bounded. A location that merely picks a
// backend keeps nginx's prefix matching, which is what people import from.
func TestRenderLocationMatchingFollowsWhoItLetsIn(t *testing.T) {
	cases := []struct {
		name    string
		d       Domain
		bounded bool
	}{
		{"open host, plain location", Domain{ID: "a", Host: "h.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y"}}}, false},
		{"open host, location asks for a login", Domain{ID: "a", Host: "h.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y", Protect: "on"}}}, false},
		{"protected host, location opens up", Domain{ID: "a", Host: "h.example.com", TargetType: "url", Target: "http://x", TLS: "none", Protect: true, Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y", Protect: "off"}}}, true},
		{"protected host with a list, location names someone else", Domain{ID: "a", Host: "h.example.com", TargetType: "url", Target: "http://x", TLS: "none", Protect: true, ProtectUsers: "alice", Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y", Protect: "on", ProtectUsers: "alice,bob"}}}, true},
		{"protected host with a list, location narrows it", Domain{ID: "a", Host: "h.example.com", TargetType: "url", Target: "http://x", TLS: "none", Protect: true, ProtectUsers: "alice,bob", Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y", Protect: "on", ProtectUsers: "alice"}}}, false},
		{"protected host, no list, location asks for anyone signed in", Domain{ID: "a", Host: "h.example.com", TargetType: "url", Target: "http://x", TLS: "none", Protect: true, Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y", Protect: "on"}}}, false},
	}
	for _, c := range cases {
		raw, err := Render([]Domain{c.d}, "http://panel:9443")
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Contains(flat(string(raw)), "(Path(`/api`) || PathPrefix(`/api/`))")
		if got != c.bounded {
			t.Errorf("%s: bounded=%v, want %v\n%s", c.name, got, c.bounded, raw)
		}
	}
}

// An app is told to trust X-Islet-User. On a route with no gate in front of it
// the header is whatever the visitor sent, so every route removes it first.
func TestRenderStripsIdentityHeaders(t *testing.T) {
	raw, err := Render([]Domain{
		{ID: "a", Host: "open.example.com", TargetType: "url", Target: "http://x", TLS: "none", Enabled: true,
			Locations: []Location{{Path: "/api", TargetType: "url", Target: "http://y"}}},
	}, "http://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	if !strings.Contains(out, "islet-strip-identity") {
		t.Fatalf("no middleware removes the identity headers:\n%s", out)
	}
	for _, rule := range []string{"rule: Host(`open.example.com`)\n", "PathPrefix(`/api`)"} {
		if b := block(out, strings.Index(out, rule)); !strings.Contains(b, "islet-strip-identity") {
			t.Errorf("a serving router keeps whatever identity the visitor sent (%s):\n%s", rule, b)
		}
	}
}
