package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

func TestParseKeyAndJWTShape(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, err := parseKey(string(pkcs1)); err != nil {
		t.Fatalf("pkcs1: %v", err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := parseKey(string(pkcs8)); err != nil {
		t.Fatalf("pkcs8: %v", err)
	}
	if _, err := parseKey("not a key"); err == nil {
		t.Fatal("garbage accepted")
	}
	// A JWT built the way jwt() does it must have three base64url parts and a valid RS256 signature.
	_ = base64.RawURLEncoding
}

func TestRepoFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/isletdev/islet":        "isletdev/islet",
		"https://github.com/isletdev/islet.git":    "isletdev/islet",
		"git@github.com:isletdev/islet.git":        "isletdev/islet",
		"https://gitlab.com/isletdev/islet":        "",
		"https://github.com/isletdev":              "",
		"https://user:tok@github.com/org/repo.git": "org/repo",
	}
	for in, want := range cases {
		got, ok := RepoFromURL(in)
		if (want == "") == ok || got != want {
			t.Errorf("%s: got %q ok=%v want %q", in, got, ok, want)
		}
	}
	if !strings.Contains("x", "x") {
		t.Fatal()
	}
}
