package proxy

import "net"

// Recognising the big reverse proxies, so that a domain sitting behind one is
// not reported as misconfigured.
//
// When Cloudflare's proxy is switched on, the A and AAAA records point at
// Cloudflare rather than at this server. That is the whole point of it, and a
// panel that answers "change the A record to 62.238.15.110" is telling the
// person to turn off the thing they deliberately turned on.
//
// The ranges are compiled in rather than fetched. They change about once a
// year, the published lists are at cloudflare.com/ips-v4 and ips-v6, and the
// cost of being out of date is a less specific message rather than a wrong
// one: an address we do not recognise simply falls back to "points somewhere
// else", which is what it did before.

type cdn struct {
	name string
	nets []*net.IPNet
}

var cdns = []cdn{
	{name: "Cloudflare", nets: mustCIDRs(
		// cloudflare.com/ips-v4
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		// cloudflare.com/ips-v6
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	)},
	{name: "Fastly", nets: mustCIDRs(
		"23.235.32.0/20", "151.101.0.0/16", "199.232.0.0/16", "2a04:4e40::/32",
	)},
}

func mustCIDRs(list ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, c := range list {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// cdnFor names the proxy an address belongs to, or "" for an ordinary address.
func cdnFor(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	for _, c := range cdns {
		for _, n := range c.nets {
			if n.Contains(ip) {
				return c.name
			}
		}
	}
	return ""
}
