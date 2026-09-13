package proxy

import "testing"

func TestParseNginx(t *testing.T) {
	cfg := `
server {
    listen 80;
    server_name example.com www.example.com;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name example.com www.example.com;
    location /api/ { proxy_pass http://127.0.0.1:3000/; }
    location / { root /var/www/site; try_files $uri /index.html; }
}
server {
    listen 80 default_server;
    server_name _;
    root /var/www/html;
}
`
	sites := ParseNginx(cfg, "test.conf")
	if len(sites) != 2 {
		t.Fatalf("got %d sites: %+v", len(sites), sites)
	}
	// The /api block forwards and the / block serves files. Reading the first
	// proxy_pass as the site's own target, which is what this used to do,
	// turned a mostly-static site into one that sends everything to :3000.
	if sites[1].Upstream != "" || !sites[1].TLS || sites[1].Root != "/var/www/site" || len(sites[1].Hosts) != 2 {
		t.Errorf("second site wrong: %+v", sites[1])
	}
	if len(sites[1].Locations) != 1 {
		t.Fatalf("want one location, got %+v", sites[1].Locations)
	}
	// proxy_pass ends in a slash, so nginx sends /api/things on as /things.
	if l := sites[1].Locations[0]; l.Path != "/api" || l.Upstream != "http://127.0.0.1:3000" || !l.StripPath {
		t.Errorf("location wrong: %+v", l)
	}
	if sites[1].NoRoot {
		t.Error("the root is served from a directory, so it is not rootless")
	}
	if sites[0].TLS || sites[0].Upstream != "" {
		t.Errorf("first site wrong: %+v", sites[0])
	}
}

// Nginx Proxy Manager writes its upstream as variables set above the
// proxy_pass, which is what most people have in front of their containers.
func TestParseNginxProxyManager(t *testing.T) {
	cfg := `
# ------------------------------------------------------------
# shop.example.com
# ------------------------------------------------------------
server {
  set $forward_scheme http;
  set $server         "islet-website";
  set $port           80;

  listen 80;
  listen [::]:80;
  listen 443 ssl http2;
  listen [::]:443 ssl http2;

  server_name shop.example.com;

  include conf.d/include/assets.conf;
  include conf.d/include/block-exploits.conf;
  include conf.d/include/letsencrypt-acme-challenge.conf;

  access_log /data/logs/proxy-host-3_access.log proxy;

  location / {
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $http_connection;
    proxy_http_version 1.1;
    add_header       X-Served-By $host;
    proxy_set_header Host $host;
    proxy_pass       $forward_scheme://$server:$port;
  }
}
server {
  set $forward_scheme https;
  set $server         "10.0.0.9";
  set $port           8443;
  listen 443 ssl;
  server_name api.example.com;
  location / { proxy_pass $forward_scheme://$server:$port; }
}
`
	sites := ParseNginx(cfg, "npm.conf")
	if len(sites) != 2 {
		t.Fatalf("got %d sites: %+v", len(sites), sites)
	}
	if sites[0].Upstream != "http://islet-website:80" || !sites[0].TLS || sites[0].Hosts[0] != "shop.example.com" {
		t.Errorf("container host wrong: %+v", sites[0])
	}
	if sites[1].Upstream != "https://10.0.0.9:8443" {
		t.Errorf("ip host wrong: %+v", sites[1])
	}
}

// A block whose upstream is still a variable must not be imported as one.
func TestParseNginxUnresolvedVariable(t *testing.T) {
	sites := ParseNginx(`server { listen 80; server_name x.example.com; location / { proxy_pass $forward_scheme://$server:$port; } }`, "f")
	if len(sites) != 1 || sites[0].Upstream != "" {
		t.Fatalf("unresolved upstream should stay empty: %+v", sites)
	}
}

func TestUpstreamParts(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{"http://islet-website:80", "islet-website", 80, true},
		{"http://app", "app", 80, true},
		{"https://app", "app", 443, true},
		{"https://10.0.0.9:8443", "10.0.0.9", 8443, true},
		{"http://app:3000/", "app", 3000, true},
		{"http://app:3000/path", "", 0, false},
		{"$forward_scheme://$server:$port", "", 0, false},
		{"unix:/tmp/app.sock", "", 0, false},
	}
	for _, c := range cases {
		h, p, ok := UpstreamParts(c.in)
		if h != c.host || p != c.port || ok != c.ok {
			t.Errorf("%s: got %q %d %v", c.in, h, p, ok)
		}
	}
}

// The real Nginx Proxy Manager template: the proxy_pass lives in an included
// snippet, so only the set variables are in the host file.
func TestParseNginxProxyManagerInclude(t *testing.T) {
	cfg := `
# ------------------------------------------------------------
# api.booxy.dev
# ------------------------------------------------------------
server {
  set $forward_scheme http;
  set $server         "booxy-api";
  set $port           4000;

  listen 80;
  listen 443 ssl http2;
  server_name api.booxy.dev;

  include conf.d/include/assets.conf;
  include conf.d/include/block-exploits.conf;
  include conf.d/include/ssl-ciphers.conf;

  access_log /data/logs/proxy-host-2_access.log proxy;

  location / {
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $http_connection;
    proxy_http_version 1.1;
    include conf.d/include/proxy.conf;
  }
}
`
	sites := ParseNginx(cfg, "2.conf")
	if len(sites) != 1 {
		t.Fatalf("got %d sites", len(sites))
	}
	if sites[0].Upstream != "http://booxy-api:4000" || sites[0].Hosts[0] != "api.booxy.dev" || !sites[0].TLS {
		t.Fatalf("wrong: %+v", sites[0])
	}
}

// A plain nginx block with no set variables and no proxy_pass stays empty.
func TestParseNginxNoUpstream(t *testing.T) {
	sites := ParseNginx(`server { listen 80; server_name plain.example.com; location / { return 204; } }`, "f")
	if len(sites) != 1 || sites[0].Upstream != "" {
		t.Fatalf("should stay empty: %+v", sites)
	}
}
