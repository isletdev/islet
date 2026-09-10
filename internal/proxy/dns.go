package proxy

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/tlsutil"
)

// DNSCheck is the DNS helper result for a host.
type DNSCheck struct {
	Host       string   `json:"host"`
	Expected   string   `json:"expected"` // this server's public IPv4
	Resolved   []string `json:"resolved"`
	OK         bool     `json:"ok"`
	Suggestion string   `json:"suggestion"`
}

// PublicIP finds the server's public IPv4: a global interface address, or
// what an external service sees when the box is behind NAT.
func PublicIP(ctx context.Context) string {
	for _, ip := range tlsutil.LocalIPs() {
		if ip.To4() != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
	}
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, http.MethodGet, "https://api.ipify.org", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(b))
}

// CheckDNS resolves host with a public resolver and compares with the
// server's public IP. It never uses the local cache so results are fresh.
func CheckDNS(ctx context.Context, host string) DNSCheck {
	out := DNSCheck{Host: host, Expected: PublicIP(ctx)}
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "udp", "1.1.1.1:53")
	}}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := r.LookupIPAddr(c, strings.TrimPrefix(host, "*."))
	if err == nil {
		for _, ip := range ips {
			out.Resolved = append(out.Resolved, ip.IP.String())
			if ip.IP.String() == out.Expected {
				out.OK = true
			}
		}
	}
	switch {
	case out.Expected == "":
		out.Suggestion = "Could not determine this server's public IP."
	case out.OK:
		out.Suggestion = "DNS points here. Certificates can be issued."
	case len(out.Resolved) == 0:
		out.Suggestion = "Create an A record: " + host + " → " + out.Expected + ". Propagation usually takes a few minutes."
	default:
		out.Suggestion = "Currently points to " + strings.Join(out.Resolved, ", ") + ". Change the A record to " + out.Expected + "."
	}
	return out
}

// PreviewHost suggests a free sslip.io hostname for this server.
func PreviewHost(ctx context.Context, name string) string {
	ip := PublicIP(ctx)
	if ip == "" {
		return ""
	}
	return name + "." + strings.ReplaceAll(ip, ".", "-") + ".sslip.io"
}

func parseLeaf(der []byte) (time.Time, string, bool) {
	// acme.json stores a PEM chain base64-encoded; take the first certificate.
	pemText := string(der)
	block := pemFirst(pemText)
	if block == nil {
		return time.Time{}, "", false
	}
	c, err := x509.ParseCertificate(block)
	if err != nil {
		return time.Time{}, "", false
	}
	return c.NotAfter, c.Issuer.CommonName, true
}
