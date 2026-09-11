package docker

import "testing"

func TestExtractRoutes(t *testing.T) {
	compose := `
services:
  web:
    image: ghcr.io/org/shop:1.2
    expose: ["3000"]
    labels:
      - traefik.enable=true
      - traefik.http.routers.shop.rule=Host(` + "`shop.example.com`" + `) || Host(` + "`www.shop.example.com`" + `)
      - traefik.http.routers.shop.tls.certresolver=letsencrypt
      - traefik.http.routers.shop-api.rule=Host(` + "`shop.example.com`" + `) && PathPrefix(` + "`/api`" + `)
  worker:
    image: ghcr.io/org/shop:1.2
    command: node worker.js
  docs:
    image: nginx
    ports:
      - "8081:80"
    labels:
      traefik.http.routers.docs.rule: Host(` + "`docs.example.com`" + `)
      traefik.http.services.docs.loadbalancer.server.port: "8080"
`
	routes := ExtractRoutes(compose)
	want := []Route{
		{Service: "docs", Host: "docs.example.com", Port: 8080},
		{Service: "web", Host: "shop.example.com", Prefix: "/api", Port: 3000},
		{Service: "web", Host: "shop.example.com", Port: 3000, TLS: true},
		{Service: "web", Host: "www.shop.example.com", Port: 3000, TLS: true},
	}
	if len(routes) != len(want) {
		t.Fatalf("got %d routes: %+v", len(routes), routes)
	}
	for i, r := range routes {
		if r != want[i] {
			t.Errorf("route %d: got %+v want %+v", i, r, want[i])
		}
	}
	if len(ExtractRoutes("services:\n  a:\n    image: x\n")) != 0 {
		t.Fatal("no labels should mean no routes")
	}
}
