package security

import (
	"slices"
	"strings"
	"testing"
)

// The published lists disagree about format and agree about comments. What
// matters most is what is thrown away.
//
// This is not hypothetical. FireHOL level 1 — the list this page offers first,
// and the safest of them — contains 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10,
// 127.0.0.0/8, 172.16.0.0/12 and 192.168.0.0/16, because it is a bogon list and
// those are bogons at the edge. Loaded into a DROP set on a Docker host they cut
// the machine off from its own containers, and 0.0.0.0/8 with it. Every one of
// them is in the table below.
func TestParseList(t *testing.T) {
	body := strings.Join([]string{
		"# a comment",
		"",
		"1.2.3.0/24",
		"5.6.7.8",
		"9.9.9.0/24 ; SBL12345 hijacked",
		"10.0.0.0/8",     // private: Docker and every home LAN
		"192.168.1.0/24", // private
		"172.17.0.0/16",  // Docker's own bridge
		"127.0.0.1",      // loopback
		"100.64.0.0/10",  // carrier-grade NAT: somebody's home connection
		"0.0.0.0/0",      // the internet
		"2.0.0.0/7",      // wider than a /8
		"224.0.0.0/4",    // multicast
		"2001:db8::/32",  // IPv6: this set is v4
		"not-an-address",
		"1.2.3.0/24", // a duplicate
		"8.8.8.0/24\t# tab separated",
	}, "\n")
	got := ParseList(body)
	want := []string{"1.2.3.0/24", "5.6.7.8/32", "8.8.8.0/24", "9.9.9.0/24"}
	if !slices.Equal(got, want) {
		t.Fatalf("ParseList =\n%v\nwant\n%v", got, want)
	}
}

// A blocklist that can drop the address of the person turning it on is a
// blocklist that locks somebody out of their own server.
func TestExcludeAddress(t *testing.T) {
	nets := []string{"1.2.3.0/24", "5.6.7.8/32", "9.9.9.0/24"}
	got := ExcludeAddress(slices.Clone(nets), "1.2.3.44")
	if slices.Contains(got, "1.2.3.0/24") {
		t.Fatalf("the admin's own network survived: %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("too much was removed: %v", got)
	}
	// An address nobody listed changes nothing, and so does an empty one —
	// a refresh has no admin in front of it.
	if len(ExcludeAddress(slices.Clone(nets), "203.0.113.9")) != 3 {
		t.Fatal("an unrelated address removed something")
	}
	if len(ExcludeAddress(slices.Clone(nets), "")) != 3 {
		t.Fatal("an empty address removed something")
	}
}

func TestSafeCIDR(t *testing.T) {
	ok := []string{"1.2.3.4", "1.2.3.0/24", "13.0.0.0/8"}
	for _, s := range ok {
		if _, good := safeCIDR(s); !good {
			t.Errorf("%q should be usable", s)
		}
	}
	bad := []string{"", "0.0.0.0/0", "1.0.0.0/7", "10.1.2.3", "172.16.5.0/24", "192.168.0.0/16",
		"169.254.1.1", "127.0.0.53", "100.100.1.1", "255.255.255.255/1", "::1", "banana"}
	for _, s := range bad {
		if _, good := safeCIDR(s); good {
			t.Errorf("%q should have been refused", s)
		}
	}
}

// The loader has to be one program, not one command per address, and it has to
// swap rather than rebuild: the rules point at a name, and a rebuild would
// leave them pointing at nothing for as long as the load takes.
//
// This exact script was run against real ipset and iptables in a container with
// its own network namespace: it loads, the rules match what is in the set, a
// second run swaps without disturbing them, and removal leaves the chain clean.
func TestRestoreScript(t *testing.T) {
	got := RestoreScript([]string{"1.2.3.0/24", "5.6.7.8/32"})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	want := []string{
		"create islet-block hash:net family inet maxelem 262144 -exist",
		"create islet-block-new hash:net family inet maxelem 262144 -exist",
		"flush islet-block-new",
		"add islet-block-new 1.2.3.0/24 -exist",
		"add islet-block-new 5.6.7.8/32 -exist",
		"swap islet-block-new islet-block",
		"destroy islet-block-new",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("script =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	// The live set is created before the swap, or the first run on a clean
	// machine fails on a name that does not exist yet.
	if !strings.HasPrefix(got, "create "+setName+" ") {
		t.Error("the live set is not created first")
	}
}
