package proxy

import "testing"

// The addresses in the bug report: a domain behind Cloudflare's proxy, which
// the check used to call a misconfiguration and tell the person to undo.
func TestCloudflareIsNotAMisconfiguration(t *testing.T) {
	for _, ip := range []string{
		"104.21.83.116", "172.67.175.142",
		"2606:4700:3030::ac43:af8e", "2606:4700:3033::6815:5374",
	} {
		if got := cdnFor(ip); got != "Cloudflare" {
			t.Errorf("cdnFor(%s) = %q, want Cloudflare", ip, got)
		}
	}
}

func TestAnOrdinaryAddressIsNotAProxy(t *testing.T) {
	for _, ip := range []string{"62.238.15.110", "91.107.213.61", "10.0.0.5", "not-an-ip"} {
		if got := cdnFor(ip); got != "" {
			t.Errorf("cdnFor(%s) = %q, want no proxy", ip, got)
		}
	}
}
