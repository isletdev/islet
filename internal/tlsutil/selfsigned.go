// Package tlsutil creates and loads the panel's own certificate. On first
// boot the daemon issues itself a self-signed certificate that covers the
// hostname, every local IP, and the sslip.io name of the public IP, so the
// panel is HTTPS from the first request. A publicly trusted certificate for
// the user's own domain comes with the proxy in a later phase.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Paths of the certificate and key inside the data directory.
const (
	CertFile = "tls/cert.pem"
	KeyFile  = "tls/key.pem"
)

// LoadOrCreate returns a TLS certificate from the data dir, creating a
// self-signed one when none exists or the existing one no longer covers the
// current hostname and addresses.
func LoadOrCreate(dataDir, hostname string) (tls.Certificate, []string, error) {
	certPath := filepath.Join(dataDir, CertFile)
	keyPath := filepath.Join(dataDir, KeyFile)
	names := SubjectNames(hostname)

	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil && covers(leaf, names) && time.Now().Before(leaf.NotAfter.Add(-30*24*time.Hour)) {
			return cert, names, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return tls.Certificate{}, nil, err
	}
	certPEM, keyPEM, err := selfSigned(names)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	return cert, names, err
}

// SubjectNames lists every name the certificate should be valid for.
func SubjectNames(hostname string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	add(hostname)
	add("localhost")
	add("127.0.0.1")
	add("::1")
	for _, ip := range LocalIPs() {
		add(ip.String())
		if ip.To4() != nil && !ip.IsPrivate() && !ip.IsLoopback() {
			add(strings.ReplaceAll(ip.String(), ".", "-") + ".sslip.io")
		}
	}
	return out
}

// LocalIPs returns the unicast addresses of all non-loopback interfaces.
func LocalIPs() []net.IP {
	var out []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() {
				out = append(out, n.IP)
			}
		}
	}
	return out
}

func covers(leaf *x509.Certificate, names []string) bool {
	have := map[string]bool{}
	for _, d := range leaf.DNSNames {
		have[d] = true
	}
	for _, ip := range leaf.IPAddresses {
		have[ip.String()] = true
	}
	for _, n := range names {
		if !have[n] {
			return false
		}
	}
	return true
}

func selfSigned(names []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Islet panel", Organization: []string{"Islet"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// Fingerprint returns the SHA-256 fingerprint of a certificate, for the
// installer to print so users can compare it in the browser warning.
func Fingerprint(cert tls.Certificate) (string, error) {
	if len(cert.Certificate) == 0 {
		return "", errors.New("empty certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", err
	}
	sum := sha256Sum(leaf.Raw)
	var b strings.Builder
	for i, c := range sum {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02X", c)
	}
	return b.String(), nil
}
