package tlsutil

import (
	"crypto/x509"
	"testing"
)

func TestLoadOrCreateIsStableAndCoversNames(t *testing.T) {
	dir := t.TempDir()
	c1, names, err := LoadOrCreate(dir, "box.example")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(c1.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !covers(leaf, names) {
		t.Fatalf("certificate does not cover %v", names)
	}
	found := false
	for _, d := range leaf.DNSNames {
		if d == "box.example" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hostname missing from SANs: %v", leaf.DNSNames)
	}
	c2, _, err := LoadOrCreate(dir, "box.example")
	if err != nil {
		t.Fatal(err)
	}
	if string(c1.Certificate[0]) != string(c2.Certificate[0]) {
		t.Fatal("certificate was regenerated on second load")
	}
	// A new hostname forces a new certificate.
	c3, _, err := LoadOrCreate(dir, "renamed.example")
	if err != nil {
		t.Fatal(err)
	}
	if string(c1.Certificate[0]) == string(c3.Certificate[0]) {
		t.Fatal("certificate was not regenerated for a new hostname")
	}
	if fp, err := Fingerprint(c3); err != nil || len(fp) != 95 {
		t.Fatalf("fingerprint = %q err=%v", fp, err)
	}
}
