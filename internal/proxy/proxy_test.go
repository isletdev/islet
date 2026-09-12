package proxy

import (
	"strings"
	"testing"
)

func TestValidateAndRender(t *testing.T) {
	d := Domain{Host: "App.Example.com", TargetType: "container", Target: "web-1", Port: 3000, BasicAuth: "alice:secret\n", IPAllowlist: "10.0.0.0/8, 203.0.113.7", RateLimit: 50, RedirectWWW: true, Headers: "X-Frame-Options: DENY", Enabled: true, ID: "abc"}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Host != "app.example.com" {
		t.Fatalf("host not normalised: %s", d.Host)
	}
	if !strings.HasPrefix(d.BasicAuth, "alice:$2") {
		t.Fatalf("password not hashed: %s", d.BasicAuth)
	}
	out, err := Render([]Domain{d, {ID: "p1", Host: "panel.example.com", TargetType: "panel", TLS: "letsencrypt", Enabled: true}, {ID: "off", Host: "off.example.com", TargetType: "container", Target: "x", Port: 80, Enabled: false}}, "https://host.docker.internal:9443")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"Host(`www.app.example.com`)", "http://web-1:3000", "certResolver: letsencrypt", "d-abc-auth", "d-abc-ipallow", "d-abc-ratelimit", "d-abc-www", "X-Frame-Options: DENY", "https://host.docker.internal:9443", "islet-insecure"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered config missing %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "off.example.com") {
		t.Error("disabled domain was rendered")
	}
	bad := Domain{Host: "not a host", TargetType: "container", Target: "x", Port: 80}
	if err := bad.Validate(); err == nil {
		t.Error("bad host accepted")
	}
	bad = Domain{Host: "a.example.com", TargetType: "container", Target: "-rm", Port: 80}
	if err := bad.Validate(); err == nil {
		t.Error("flag-like target accepted")
	}
}
