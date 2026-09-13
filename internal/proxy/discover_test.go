package proxy

import "testing"

func hostsOf(sites []Site) []string {
	var out []string
	for _, s := range sites {
		out = append(out, s.Hosts...)
	}
	return out
}

func TestParseCaddy(t *testing.T) {
	const text = `
{
	email me@example.com
}

(common) {
	encode gzip
}

example.com, www.example.com {
	import common
	reverse_proxy localhost:3000
}

http://plain.example.org {
	root * /srv/plain
	file_server
}

:8080 {
	respond "no name here"
}
`
	sites := ParseCaddy(text, "Caddyfile")
	if len(sites) != 2 {
		t.Fatalf("got %d sites (%v), want 2: the global block, the snippet and the port-only block are not sites",
			len(sites), hostsOf(sites))
	}
	if got := sites[0].Hosts; len(got) != 2 || got[0] != "example.com" || got[1] != "www.example.com" {
		t.Errorf("hosts = %v", got)
	}
	if sites[0].Upstream != "http://localhost:3000" {
		t.Errorf("upstream = %q", sites[0].Upstream)
	}
	if !sites[0].TLS {
		t.Error("caddy serves https by default, so this one has TLS")
	}
	if sites[1].Root != "/srv/plain" {
		t.Errorf("root = %q", sites[1].Root)
	}
	if sites[1].TLS {
		t.Error("an http:// address is not TLS")
	}
}

func TestParseApache(t *testing.T) {
	const text = `
<VirtualHost *:443>
    ServerName app.example.com
    ServerAlias www.app.example.com
    SSLEngine on
    ProxyPass / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/
</VirtualHost>

<VirtualHost *:80>
    # ServerName commented.example.com
    ServerName static.example.com
    DocumentRoot /var/www/static
</VirtualHost>
`
	sites := ParseApache(text, "sites-enabled/000")
	if len(sites) != 2 {
		t.Fatalf("got %d sites, want 2", len(sites))
	}
	if got := sites[0].Hosts; len(got) != 2 || got[0] != "app.example.com" {
		t.Errorf("hosts = %v", got)
	}
	if sites[0].Upstream != "http://127.0.0.1:8080" {
		t.Errorf("upstream = %q, want the trailing path dropped", sites[0].Upstream)
	}
	if !sites[0].TLS {
		t.Error("SSLEngine on means TLS")
	}
	if got := sites[1].Hosts; len(got) != 1 || got[0] != "static.example.com" {
		t.Errorf("a commented ServerName is not a host: %v", got)
	}
	if sites[1].Root != "/var/www/static" {
		t.Errorf("root = %q", sites[1].Root)
	}
}

// Pasting a file should not also require saying what it is.
func TestParseTextRecognisesTheFormat(t *testing.T) {
	cases := []struct{ text, want string }{
		{"<VirtualHost *:80>\nServerName a.example.com\n</VirtualHost>", "Apache"},
		{"server {\n server_name a.example.com;\n proxy_pass http://app:3000;\n}", "nginx"},
		{"server {\n set $forward_scheme http;\n set $server \"app\";\n server_name a.example.com;\n proxy_pass $forward_scheme://$server:3000;\n}", "Nginx Proxy Manager"},
		{"a.example.com {\n reverse_proxy 127.0.0.1:3000\n}", "Caddy"},
	}
	for _, c := range cases {
		sites, src := ParseText(c.text)
		if src != c.want {
			t.Errorf("source = %q, want %q for %.30q", src, c.want, c.text)
		}
		if len(sites) == 0 {
			t.Errorf("no sites found in %.40q", c.text)
			continue
		}
		// The source travels with the site: a note that says which product a
		// host came out of is only right if every parser sets it.
		if sites[0].Source != c.want {
			t.Errorf("site source = %q, want %q", sites[0].Source, c.want)
		}
	}
}
