package security

import (
	"slices"
	"strings"
	"testing"
)

// The blocklist must only drop connections somebody else opens.
//
// A DROP on a source address matches every packet from that address, and the
// replies to a connection this server opened are packets from that address. So
// the rule as first written stopped the server from talking to anything a list
// happened to name — and the first thing it broke was the blocklist itself:
// ipdeny.com, where the country zone files come from, resolves to an address
// that appears in IPsum. Turning country blocks on made country blocks
// impossible to download.
//
// It hid well, because the failure is intermittent by construction. The fetch
// runs before the new set is loaded, so the refresh that first blocks the
// source still succeeds; only the next one fails, and it falls back to the
// cached copy, which looks like a working blocklist from the outside.
func TestTheBlocklistOnlyDropsNewConnections(t *testing.T) {
	for _, chain := range []string{"INPUT", "DOCKER-USER"} {
		args := ruleArgs(chain)
		if args[0] != chain {
			t.Errorf("%s: the chain has to come first, got %q", chain, args[0])
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--ctstate NEW") {
			t.Errorf("%s: a stateless DROP also drops the replies to our own requests: %s", chain, joined)
		}
		if !strings.Contains(joined, "--match-set "+setName+" src") {
			t.Errorf("%s: the rule no longer matches the blocklist set: %s", chain, joined)
		}
		if !slices.Contains(args, "DROP") {
			t.Errorf("%s: the rule does not drop anything: %s", chain, joined)
		}
	}
}

// And the stateless rule an older daemon installed has to be removable, or an
// upgrade leaves it in place next to the new one, still dropping the replies
// this exists to allow.
func TestTheOldStatelessRuleIsStillRecognised(t *testing.T) {
	old := strings.Join(legacyRuleArgs("INPUT"), " ")
	if strings.Contains(old, "ctstate") {
		t.Errorf("the legacy rule is meant to be the stateless one: %s", old)
	}
	if !strings.Contains(old, "--match-set "+setName+" src") {
		t.Errorf("the legacy rule would not match what old daemons installed: %s", old)
	}
}
