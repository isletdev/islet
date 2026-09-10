package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cand, cur string
		want      bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.0", "0.2.0", false},
		{"v1.0.0", "0.9.9", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.1.0-beta.1", true},
		{"0.1.0-beta.1", "0.1.0", false},
		{"0.0.1", "dev", true},
	}
	for _, c := range cases {
		if got := IsNewer(c.cand, c.cur); got != c.want {
			t.Errorf("IsNewer(%q,%q)=%v want %v", c.cand, c.cur, got, c.want)
		}
	}
}

func TestVerifySignatureRejectsWrongKeyAndTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	old := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(pub)
	defer func() { PublicKey = old }()

	data := []byte("abc  isletd_linux_amd64.tar.gz\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
	if err := VerifySignature(data, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifySignature([]byte("tampered"), sig); err == nil {
		t.Fatal("tampered data accepted")
	}
	_, other, _ := ed25519.GenerateKey(nil)
	bad := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(other, data)))
	if err := VerifySignature(data, bad); err == nil {
		t.Fatal("signature from another key accepted")
	}
}

func TestChecksumFor(t *testing.T) {
	sums := []byte("aa11  isletd_linux_amd64.tar.gz\nbb22  isletd_linux_arm64.tar.gz\n")
	if v, err := checksumFor(sums, "isletd_linux_arm64.tar.gz"); err != nil || v != "bb22" {
		t.Fatalf("got %q %v", v, err)
	}
	if _, err := checksumFor(sums, "nope"); err == nil {
		t.Fatal("missing asset accepted")
	}
}
