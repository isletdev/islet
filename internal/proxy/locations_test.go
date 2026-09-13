package proxy

import (
	"strings"
	"testing"
)

// One hostname forwarding several paths to several backends is the ordinary
// shape in every product people arrive from, and reading only the first
// proxy_pass turned such a host into an import that looked complete and was
// not. These check that each format's own way of saying it survives, including
// the part everyone forgets: whether the prefix reaches the backend.

func TestParseNginxLocations(t *testing.T) {
	cfg := `
server {
    listen 443 ssl;
    server_name shop.example.com;
    location / { proxy_pass http://web:8080; }
    location /api { proxy_pass http://api:3000; }
    location /static/ { proxy_pass http://cdn:80/; }
    location = /health { proxy_pass http://api:3000; }
    location ~ \.php$ { proxy_pass http://php:9000; }
    location @fallback { proxy_pass http://web:8080; }
}
`
	sites := ParseNginx(cfg, "shop.conf")
	if len(sites) != 1 {
		t.Fatalf("got %d sites", len(sites))
	}
	s := sites[0]
	if s.Upstream != "http://web:8080" || s.RootStrip {
		t.Errorf("root wrong: %q strip=%v", s.Upstream, s.RootStrip)
	}
	want := []SiteLocation{
		{Path: "/api", Upstream: "http://api:3000", StripPath: false},
		{Path: "/static", Upstream: "http://cdn:80", StripPath: true},
	}
	if len(s.Locations) != len(want) {
		t.Fatalf("locations = %+v", s.Locations)
	}
	for i, w := range want {
		if s.Locations[i] != w {
			t.Errorf("location %d = %+v, want %+v", i, s.Locations[i], w)
		}
	}
	// The three that cannot be translated are named rather than dropped in
	// silence: a person who is told can go and recreate them.
	if len(s.Skipped) != 3 {
		t.Errorf("skipped = %+v, want the exact, regex and named blocks", s.Skipped)
	}
	if s.NoRoot {
		t.Error("location / forwards, so the root is not missing")
	}
}

// A host that only forwards on paths. nginx allows it and Islet needs a root
// target, so the parser has to say the root is missing rather than invent one.
func TestParseNginxNoRoot(t *testing.T) {
	cfg := `
server {
    server_name api.example.com;
    location /v1 { proxy_pass http://v1:8000; }
    location /v2 { proxy_pass http://v2:8000; }
}
`
	s := ParseNginx(cfg, "api.conf")[0]
	if !s.NoRoot {
		t.Errorf("want NoRoot, got %+v", s)
	}
	if len(s.Locations) != 2 {
		t.Fatalf("locations = %+v", s.Locations)
	}
}

// Nginx Proxy Manager writes each custom location as its own block with its
// own $server and $port, and keeps the proxy_pass in an included snippet. The
// variables are the only machine-readable copy of where a location points.
func TestParseNginxProxyManagerLocations(t *testing.T) {
	cfg := `
server {
  set $forward_scheme http;
  set $server         "web";
  set $port           8080;
  listen 443 ssl;
  server_name shop.example.com;
  include conf.d/include/proxy.conf;

  location /api {
    set $forward_scheme http;
    set $server         "api";
    set $port           3000;
    include conf.d/include/proxy.conf;
  }
}
`
	s := ParseNginx(cfg, "1.conf")[0]
	if s.Upstream != "http://web:8080" {
		t.Errorf("root upstream = %q", s.Upstream)
	}
	if len(s.Locations) != 1 || s.Locations[0].Path != "/api" || s.Locations[0].Upstream != "http://api:3000" {
		t.Fatalf("locations = %+v", s.Locations)
	}
}

// A location's proxy_pass must not be read as the server's. The server here
// has none of its own, and the root has to come from `location /`.
func TestParseNginxLocationDoesNotLeak(t *testing.T) {
	cfg := `
server {
    server_name one.example.com;
    location /only { proxy_pass http://inner:9000; }
}
server {
    server_name two.example.com;
    proxy_pass http://outer:9001;
}
`
	sites := ParseNginx(cfg, "two.conf")
	if sites[0].Upstream != "" {
		t.Errorf("the location leaked into the site: %+v", sites[0])
	}
	if sites[1].Upstream != "http://outer:9001" {
		t.Errorf("server-level proxy_pass lost: %+v", sites[1])
	}
}

func TestParseCaddyLocations(t *testing.T) {
	cfg := `
shop.example.com {
	handle_path /static/* {
		reverse_proxy cdn:80
	}
	handle /api/* {
		reverse_proxy api:3000
	}
	reverse_proxy web:8080
}
`
	s := ParseCaddy(cfg, "Caddyfile")[0]
	if s.Upstream != "http://web:8080" {
		t.Errorf("root = %q", s.Upstream)
	}
	got := map[string]SiteLocation{}
	for _, l := range s.Locations {
		got[l.Path] = l
	}
	if l := got["/static"]; l.Upstream != "http://cdn:80" || !l.StripPath {
		t.Errorf("handle_path should strip: %+v", l)
	}
	if l := got["/api"]; l.Upstream != "http://api:3000" || l.StripPath {
		t.Errorf("handle should not strip: %+v", l)
	}
}

// The inline matcher form, which is how short Caddyfiles are usually written.
func TestParseCaddyInlineMatcher(t *testing.T) {
	cfg := `
shop.example.com {
	reverse_proxy /api/* api:3000
	reverse_proxy web:8080
}
`
	s := ParseCaddy(cfg, "Caddyfile")[0]
	if s.Upstream != "http://web:8080" {
		t.Errorf("root = %q", s.Upstream)
	}
	if len(s.Locations) != 1 || s.Locations[0].Path != "/api" || s.Locations[0].StripPath {
		t.Fatalf("locations = %+v", s.Locations)
	}
}

// `handle /*` is how a Caddyfile spells its own root when the rest of the
// site is in handle blocks. It is not a matcher that failed to parse, and
// filing it under the skipped ones would leave the site with no target.
func TestParseCaddyRootHandle(t *testing.T) {
	cfg := `
shop.example.com {
	handle /api/* {
		reverse_proxy api:3000
	}
	handle /* {
		reverse_proxy web:8080
	}
}
`
	s := ParseCaddy(cfg, "Caddyfile")[0]
	if s.Upstream != "http://web:8080" {
		t.Errorf("root = %q, skipped = %v", s.Upstream, s.Skipped)
	}
	if len(s.Skipped) != 0 {
		t.Errorf("nothing here is untranslatable, got %v", s.Skipped)
	}
	if len(s.Locations) != 1 || s.Locations[0].Path != "/api" {
		t.Errorf("locations = %+v", s.Locations)
	}
	if s.NoRoot {
		t.Error("the root is handled")
	}
}

func TestParseApacheLocations(t *testing.T) {
	cfg := `
<VirtualHost *:443>
    ServerName shop.example.com
    SSLEngine on
    ProxyPass /api http://api:3000/
    ProxyPass /keep http://other:4000/keep
    ProxyPass / http://web:8080/
</VirtualHost>
`
	s := ParseApache(cfg, "shop.conf")[0]
	if s.Upstream != "http://web:8080" {
		t.Errorf("root = %q", s.Upstream)
	}
	got := map[string]SiteLocation{}
	for _, l := range s.Locations {
		got[l.Path] = l
	}
	// A target ending at the root replaces the prefix; one that repeats the
	// prefix keeps it.
	if l := got["/api"]; l.Upstream != "http://api:3000" || !l.StripPath {
		t.Errorf("/api should strip: %+v", l)
	}
	if l := got["/keep"]; l.StripPath {
		t.Errorf("/keep should not strip: %+v", l)
	}
}

// Round-trip: what the parser finds has to survive Validate, or an import
// produces rows the panel then refuses to save.
func TestLocationValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Location
		ok   bool
		want string
	}{
		{"plain", Location{Path: "/api", TargetType: "container", Target: "api", Port: 3000}, true, "/api"},
		{"trailing slash is trimmed", Location{Path: "/api/", TargetType: "url", Target: "http://x"}, true, "/api"},
		{"root is the domain's own", Location{Path: "/", TargetType: "url", Target: "http://x"}, false, ""},
		{"needs a leading slash", Location{Path: "api", TargetType: "url", Target: "http://x"}, false, ""},
		// The rule is built by interpolating the path between backticks.
		{"no backtick", Location{Path: "/a`)||Host(`evil.test", TargetType: "url", Target: "http://x"}, false, ""},
		{"no space", Location{Path: "/a b", TargetType: "url", Target: "http://x"}, false, ""},
		{"target must be real", Location{Path: "/api", TargetType: "url", Target: "ftp://x"}, false, ""},
	} {
		l := tc.in
		err := l.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: accepted %+v", tc.name, tc.in)
		}
		if tc.ok && l.Path != tc.want {
			t.Errorf("%s: path = %q, want %q", tc.name, l.Path, tc.want)
		}
	}
}

// Every router on a host has to present the same certificate, a location
// has to outrank the root it sits under, and an exact host has to outrank a
// wildcard however long the wildcard's path is.
func TestRenderLocations(t *testing.T) {
	out, err := Render([]Domain{
		{
			ID: "a", Host: "shop.example.com", TargetType: "container", Target: "web", Port: 8080,
			TLS: "letsencrypt-dns", Enabled: true,
			Locations: []Location{
				{ID: "l1", Path: "/api", TargetType: "container", Target: "api", Port: 3000},
				{ID: "l2", Path: "/static", TargetType: "url", Target: "http://cdn", StripPath: true},
			},
		},
	}, "https://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out)
	for _, want := range []string{
		"d-a-l0", "d-a-l1",
		"PathPrefix(`/api`)", "PathPrefix(`/static`)",
		"stripPrefix", "certResolver: letsencrypt-dns",
		"http://api:3000", "http://cdn",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("rendered config is missing %q\n%s", want, y)
		}
	}
	// One resolver per host: three routers on this host, each with TLS.
	if n := strings.Count(y, "certResolver: letsencrypt-dns"); n < 3 {
		t.Errorf("want every router on the host to use the DNS resolver, got %d\n%s", n, y)
	}
	if strings.Contains(y, "certResolver: letsencrypt\n") {
		t.Errorf("a router fell back to the HTTP resolver\n%s", y)
	}
}

func TestRenderLocationPriority(t *testing.T) {
	out, err := Render([]Domain{
		{ID: "w", Host: "*.example.com", TargetType: "container", Target: "w", Port: 80, TLS: "letsencrypt-dns", Enabled: true,
			Locations: []Location{{ID: "wl", Path: "/a-very-long-path-that-would-otherwise-win", TargetType: "url", Target: "http://x"}}},
		{ID: "e", Host: "shop.example.com", TargetType: "container", Target: "e", Port: 80, TLS: "letsencrypt", Enabled: true,
			Locations: []Location{{ID: "el", Path: "/api", TargetType: "url", Target: "http://y"}}},
	}, "https://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	pri := map[string]int{}
	var name string
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasSuffix(t, ":") && strings.HasPrefix(t, "d-") {
			name = strings.TrimSuffix(t, ":")
		}
		if v, ok := strings.CutPrefix(t, "priority: "); ok && name != "" {
			n := 0
			for _, c := range v {
				n = n*10 + int(c-'0')
			}
			pri[name] = n
		}
	}
	if pri["d-e-l0"] <= pri["d-e"] {
		t.Errorf("a location must outrank its own root: %v", pri)
	}
	if pri["d-w-l0"] >= pri["d-e"] {
		t.Errorf("a wildcard's location must not outrank an exact host: %v", pri)
	}
}
