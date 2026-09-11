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
	if sites[1].Upstream != "http://127.0.0.1:3000" || !sites[1].TLS || sites[1].Root != "/var/www/site" || len(sites[1].Hosts) != 2 {
		t.Errorf("second site wrong: %+v", sites[1])
	}
	if sites[0].TLS || sites[0].Upstream != "" {
		t.Errorf("first site wrong: %+v", sites[0])
	}
}
