package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
)

// Routes that only "*" may reach, on purpose. Accounts and tokens are here
// because no narrow scope may widen itself; anything else appearing in this
// list is a route nobody can use with a scoped token, which is usually a
// mistake rather than a decision.
var starOnly = []string{
	"/api/v1/auth",
	"/api/v1/users",
}

// Every authenticated route has to be reachable by some scope. A route in no
// area is denied to every token by the default at the end of ScopeAllows, and
// nothing says so: it simply never works, and the person holding the token is
// told their scopes do not cover it without being told which would.
func TestEveryAuthenticatedRouteHasAScope(t *testing.T) {
	b, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("cannot read the route table: %v", err)
	}
	re := regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+) (/api/v1/[^"]*)"(.*)`)
	orphans := map[string]string{}
	checked := 0
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		method, path, tail := m[1], m[2], m[3]
		// Public routes never consult ScopeAllows: webhooks carry their own
		// signature, the heartbeat URL is the secret, setup runs once.
		if !strings.Contains(tail, "requireAuth") {
			continue
		}
		// Path patterns are matched by prefix, and every rule matches before
		// the first placeholder, so the template is what to test.
		if i := strings.Index(path, "{"); i > 0 {
			path = path[:i]
		}
		path = strings.TrimSuffix(path, "/")
		skip := false
		for _, s := range starOnly {
			if strings.HasPrefix(path, s) {
				skip = true
			}
		}
		if skip {
			continue
		}
		checked++
		reachable := false
		for _, sc := range auth.Scopes {
			if auth.ScopeAllows(sc, method, path) {
				reachable = true
				break
			}
		}
		if !reachable {
			orphans[method+" "+path] = ""
		}
	}
	if checked < 50 {
		t.Fatalf("only found %d routes to check; the parser has probably stopped matching", checked)
	}
	if len(orphans) > 0 {
		var list []string
		for k := range orphans {
			list = append(list, k)
		}
		sort.Strings(list)
		t.Errorf("no scope can reach these routes, so no token can use them:\n  %s", strings.Join(list, "\n  "))
	}
}

// And the other direction: the star-only list should stay short and
// deliberate. If it grows, something is being hidden from scoped tokens.
func TestStarOnlyListStaysSmall(t *testing.T) {
	if len(starOnly) > 3 {
		t.Errorf("the *-only list has grown to %d entries: %v", len(starOnly), starOnly)
	}
	for _, p := range starOnly {
		if auth.ScopeAllows("read", "GET", p+"/anything") {
			t.Errorf("%s is meant to be reachable only with *", p)
		}
	}
}
