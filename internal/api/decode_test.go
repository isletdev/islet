package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// A request whose length is unknown still has a body.
//
// ContentLength is -1 for a chunked request, and a chunked request is what
// arrives once the panel is reached through a reverse proxy — including
// Islet's own Traefik, as soon as the panel has a domain. A handler that asked
// "is ContentLength > 0" before reading therefore ignored the body for exactly
// those users: every field arrived at its zero value, and the call answered
// 200 as though it had been given what it asked for. The proxy was rebuilt
// with no ACME email, so no certificate resolver, so every site on the server
// was served a self-signed certificate.
func TestDecodeReadsBodyWithUnknownLength(t *testing.T) {
	var got struct {
		AcmeEmail   string            `json:"acmeEmail"`
		DNSProvider *string           `json:"dnsProvider"`
		DNSEnv      map[string]string `json:"dnsEnv"`
	}
	body := `{"acmeEmail":"lev@example.com","dnsProvider":"cloudflare","dnsEnv":{"CF_DNS_API_TOKEN":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/proxy/install", strings.NewReader(body))
	r.ContentLength = -1 // what a chunked request looks like
	r.Header.Set("Content-Type", "application/json")

	if err := decode(r, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AcmeEmail != "lev@example.com" {
		t.Errorf("acmeEmail = %q, want the address that was sent", got.AcmeEmail)
	}
	if got.DNSProvider == nil || *got.DNSProvider != "cloudflare" {
		t.Errorf("dnsProvider = %v", got.DNSProvider)
	}
	if got.DNSEnv["CF_DNS_API_TOKEN"] != "t" {
		t.Errorf("credentials were dropped: %v", got.DNSEnv)
	}
}

// A body-less POST is a legitimate way to say "do it again with what you have",
// and must be told apart from a malformed one.
func TestDecodeEmptyBodyIsEOF(t *testing.T) {
	var got struct {
		AcmeEmail string `json:"acmeEmail"`
	}
	r := httptest.NewRequest("POST", "/api/v1/proxy/install", strings.NewReader(""))
	r.ContentLength = -1
	err := decode(r, &got)
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF so the caller can tell an empty body from a broken one", err)
	}
}
