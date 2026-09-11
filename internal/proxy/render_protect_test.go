package proxy

import (
	"strings"
	"testing"
)

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
	// the router block precedes its rule line; find the block by working back from the rule
	block := func(idx int) string {
		start := strings.LastIndex(out[:idx], "\n        d-")
		return out[start:idx]
	}
	if !strings.Contains(block(i), "islet-forward-auth") {
		t.Fatalf("protected router lacks forward auth:\n%s", block(i))
	}
	if strings.Contains(block(j), "islet-forward-auth") {
		t.Fatalf("open router has forward auth:\n%s", block(j))
	}
}
