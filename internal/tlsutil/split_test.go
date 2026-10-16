package tlsutil

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestSplitServesBothSchemesOnOnePort(t *testing.T) {
	cert, _, err := LoadOrCreate(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	plain := false
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}
	panel, other := Split(ln, cfg, func(ip netip.Addr) bool { return plain && ip.IsLoopback() })
	srv := &http.Server{TLSConfig: cfg, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, map[bool]string{true: "tls", false: "plain"}[r.TLS != nil]+" "+r.Proto)
	})}
	go func() { _ = srv.Serve(panel) }()
	go func() {
		_ = (&http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusPermanentRedirect)
		})}).Serve(other)
	}()
	defer srv.Close()
	addr := ln.Addr().String()

	// A client that connects and says nothing must not stop the next one.
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()

	get := func(c *http.Client, u string) (int, string) {
		t.Helper()
		res, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	noFollow := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}
	secure := &http.Client{Timeout: 5 * time.Second, Transport: tr}

	if code, body := get(secure, "https://"+addr+"/"); code != 200 || body != "tls HTTP/2.0" {
		t.Errorf("https: %d %q", code, body)
	}
	if code, _ := get(noFollow, "http://"+addr+"/x?y=1"); code != http.StatusPermanentRedirect {
		t.Errorf("plain HTTP from outside the tunnel: %d, want a redirect", code)
	}
	plain = true
	if code, body := get(noFollow, "http://"+addr+"/"); code != 200 || !strings.HasPrefix(body, "plain") {
		t.Errorf("plain HTTP from inside the tunnel: %d %q", code, body)
	}
	if code, body := get(secure, "https://"+addr+"/"); code != 200 || body != "tls HTTP/2.0" {
		t.Errorf("https after plain: %d %q", code, body)
	}
}
