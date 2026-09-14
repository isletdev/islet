package proxy

import (
	"strings"
	"testing"
)

// What an app is told the scheme was.
//
// Traefik sets X-Forwarded-Proto to "ws" or "wss" on a WebSocket upgrade
// rather than to the scheme the browser used. Almost nothing downstream
// understands those values: Plug.SSL, Rails, Django and Laravel all compare
// against "https", find "wss", decide the connection is plain, and redirect to
// HTTPS — which is where the request already came from. The socket dies in a
// redirect loop while every ordinary request on the same host is fine, so the
// symptom looks like "WebSockets are broken" and nothing in the proxy looks
// wrong.
//
// Islet knows which scheme each route answers on, so it states it, and the
// value no longer depends on whether the request happens to upgrade.
func TestForwardedProtoIsStated(t *testing.T) {
	for _, tc := range []struct {
		name string
		tls  string
		want string
	}{
		{"a TLS host says https", "letsencrypt", "islet-proto-https"},
		{"a DNS-issued host says https", "letsencrypt-dns", "islet-proto-https"},
		{"a self-signed host says https", "self", "islet-proto-https"},
		{"a plain HTTP host says http", "none", "islet-proto-http"},
	} {
		out, err := Render([]Domain{{
			ID: "d", Host: "app.example.com", TargetType: "container", Target: "web", Port: 80,
			TLS: tc.tls, Enabled: true, PassHost: true,
			Locations: []Location{{ID: "l1", Path: "/socket", TargetType: "container", Target: "rt", Port: 4001}},
		}}, "https://panel:9443")
		if err != nil {
			t.Fatal(err)
		}
		y := string(out)
		if !strings.Contains(y, tc.want) {
			t.Errorf("%s: %q missing\n%s", tc.name, tc.want, y)
		}
		// The root and the location both, or a socket mounted on a path is
		// still told the wrong thing.
		if n := strings.Count(y, "- "+tc.want); n < 2 {
			t.Errorf("%s: the location did not get it (%d uses)\n%s", tc.name, n, y)
		}
	}

	// Both middlewares are always defined, and each states one scheme.
	out, _ := Render([]Domain{{
		ID: "d", Host: "app.example.com", TargetType: "container", Target: "web", Port: 80,
		TLS: "letsencrypt", Enabled: true, PassHost: true,
	}}, "https://panel:9443")
	y := string(out)
	for _, want := range []string{"islet-proto-https", "islet-proto-http", "X-Forwarded-Proto"} {
		if !strings.Contains(y, want) {
			t.Errorf("missing %q in the rendered config", want)
		}
	}
	// It has to run before anything that reads the scheme.
	i := strings.Index(y, "- islet-proto-https")
	j := strings.Index(y, "- islet-security-headers")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the scheme must be corrected before the headers middleware reads it (%d, %d)", i, j)
	}
}
