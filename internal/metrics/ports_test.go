package metrics

import "testing"

// One row per socket is what the kernel has and not what anybody can read. On
// the development server a TURN daemon bound to every interface was twelve rows
// of port 3478 — public v4, public v6, loopback, and each Docker bridge — and
// anything listening on both families was two rows of one thing: forty-seven
// lines for twenty-five ports.
//
// Collapsing them must not hide exposure, which is the only reason the address
// is kept at all.
func TestTheAddressKeptIsTheMostExposedOne(t *testing.T) {
	cases := []struct {
		name  string
		addrs []string
		want  string
	}{
		{"a wildcard beats anything else", []string{"127.0.0.1", "172.17.0.1", "0.0.0.0"}, "0.0.0.0"},
		{"v6 wildcard counts as one", []string{"::1", "::"}, "::"},
		{"a public address beats a bridge", []string{"172.18.0.1", "5.75.226.252"}, "5.75.226.252"},
		{"a bridge beats loopback", []string{"127.0.0.1", "172.22.0.1"}, "172.22.0.1"},
		{"loopback only stays loopback", []string{"127.0.0.1", "::1"}, "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			best := c.addrs[0]
			for _, a := range c.addrs[1:] {
				if exposure(a) > exposure(best) {
					best = a
				}
			}
			if best != c.want {
				t.Errorf("kept %q, want %q — a port that is reachable from outside must not read as private", best, c.want)
			}
		})
	}
}

// The ranking itself, since everything above rests on it.
func TestExposureRanking(t *testing.T) {
	if exposure("0.0.0.0") <= exposure("5.75.226.252") {
		t.Error("a wildcard is at least as exposed as any one address: it covers interfaces added later too")
	}
	if exposure("5.75.226.252") <= exposure("172.17.0.1") {
		t.Error("a public address is more exposed than a Docker bridge")
	}
	if exposure("172.17.0.1") <= exposure("127.0.0.1") {
		t.Error("a bridge is reachable from containers; loopback is not reachable at all")
	}
	if exposure("::1") != exposure("127.0.0.1") {
		t.Error("both loopbacks are the same thing")
	}
	// An address that will not parse is not assumed harmless.
	if exposure("not-an-ip") <= exposure("127.0.0.1") {
		t.Error("something unparseable should not rank below loopback")
	}
}
