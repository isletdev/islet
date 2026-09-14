package proxy

import (
	"strings"
	"testing"
)

// Which hostname the app behind the proxy is told about.
//
// nginx and Nginx Proxy Manager both forward the visitor's, and Islet did that
// for a container target and the opposite for a URL target — which is what an
// imported site almost always becomes. Anything that reads its own hostname
// then saw the upstream's address instead: WebSocket origin checks refuse on
// that basis, and absolute redirects, cookie domains and generated links go
// quietly wrong.
func TestPassHostReachesTheService(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    Domain
		want string
	}{
		{"url target passes the host", Domain{
			ID: "u", Host: "app.example.com", TargetType: "url", Target: "http://10.0.0.5:8080",
			TLS: "self", Enabled: true, PassHost: true}, "passHostHeader: true"},
		{"url target can still rewrite it", Domain{
			ID: "u", Host: "app.example.com", TargetType: "url", Target: "https://api.stripe.com",
			TLS: "self", Enabled: true, PassHost: false}, "passHostHeader: false"},
		{"a location inherits the host setting", Domain{
			ID: "l", Host: "app.example.com", TargetType: "url", Target: "http://10.0.0.5:8080",
			TLS: "self", Enabled: true, PassHost: true,
			Locations: []Location{{ID: "l1", Path: "/api", TargetType: "url", Target: "http://10.0.0.6:3000"}},
		}, "passHostHeader: true"},
	} {
		out, err := Render([]Domain{tc.d}, "https://panel:9443")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: want %q in\n%s", tc.name, tc.want, out)
		}
	}

	// The location's own service, not just the root's.
	out, _ := Render([]Domain{{
		ID: "l", Host: "app.example.com", TargetType: "url", Target: "http://10.0.0.5:8080",
		TLS: "self", Enabled: true, PassHost: true,
		Locations: []Location{{ID: "l1", Path: "/api", TargetType: "url", Target: "http://10.0.0.6:3000"}},
	}}, "https://panel:9443")
	if n := strings.Count(string(out), "passHostHeader: true"); n < 2 {
		t.Errorf("the location's service did not inherit it (%d)\n%s", n, out)
	}
}

// The blocker has to outrank everything on its host, including a location, and
// has to leave /.well-known alone or certificates stop being issued.
func TestBlockExploitsRouter(t *testing.T) {
	out, err := Render([]Domain{{
		ID: "b", Host: "app.example.com", TargetType: "container", Target: "web", Port: 80,
		TLS: "self", Enabled: true, PassHost: true, BlockExploits: true,
		Locations: []Location{{ID: "l1", Path: "/api", TargetType: "url", Target: "http://10.0.0.6:3000"}},
	}}, "https://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out)
	if !strings.Contains(y, "d-b-block") {
		t.Fatalf("no blocker router:\n%s", y)
	}
	if !strings.Contains(y, "/_islet/blocked") {
		t.Errorf("the blocker does not answer from the panel:\n%s", y)
	}
	// The rule must not mention .well-known at all: it is allowed by being
	// absent from every pattern, and a pattern that named it would be a bug.
	if strings.Contains(y, "well-known") {
		t.Errorf("the blocker rule mentions .well-known, which ACME needs:\n%s", y)
	}
	for _, want := range []string{"env|git|svn", "vendor/phpunit", "TRACE", "TRACK"} {
		if !strings.Contains(y, want) {
			t.Errorf("blocker rule is missing %q", want)
		}
	}

	// Off by default, and off means no router at all.
	out, _ = Render([]Domain{{
		ID: "p", Host: "plain.example.com", TargetType: "container", Target: "web", Port: 80,
		TLS: "self", Enabled: true, PassHost: true,
	}}, "https://panel:9443")
	if strings.Contains(string(out), "-block") {
		t.Errorf("a domain that did not ask for it got a blocker:\n%s", out)
	}
}

// What the source configuration already decided has to survive the import.
func TestParseNginxCarriesFlags(t *testing.T) {
	npm := `
server {
  set $forward_scheme http;
  set $server "10.0.0.5";
  set $port 8080;
  server_name app.example.com;
  proxy_set_header Host $host;
  proxy_set_header Upgrade $http_upgrade;
  include conf.d/include/block-exploits.conf;
  include conf.d/include/proxy.conf;
}
`
	s := ParseNginx(npm, "1.conf")[0]
	if !s.PassHost || !s.BlockExploits || !s.WebSockets {
		t.Errorf("flags lost: %+v", s)
	}

	// A Host naming something other than the visitor's is the one case that
	// means "rewrite it".
	rewritten := `
server {
  server_name app.example.com;
  proxy_set_header Host backend.internal;
  location / { proxy_pass http://10.0.0.5:8080; }
}
`
	if s := ParseNginx(rewritten, "2.conf")[0]; s.PassHost {
		t.Errorf("an explicit Host was read as passing the visitor's: %+v", s)
	}

	// No Host line at all still means the visitor's, because that is nginx's
	// own default in every configuration people actually write.
	bare := `
server {
  server_name app.example.com;
  location / { proxy_pass http://10.0.0.5:8080; }
}
`
	if s := ParseNginx(bare, "3.conf")[0]; !s.PassHost {
		t.Errorf("the default should be to pass the visitor's host: %+v", s)
	}
}
