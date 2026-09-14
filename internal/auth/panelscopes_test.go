package auth

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The panel's scope list is written by hand, as every type here is. This is the
// guard: a scope offered in the panel that no rule understands would grant
// nothing and quietly produce a useless token, and a scope the rules know that
// the panel never offers cannot be given to anyone at all.
//
// The same idea as hack/sql-split-agree.mjs — two implementations of one fact,
// checked against each other rather than trusted.
func TestPanelOffersTheSameScopes(t *testing.T) {
	const panel = "../../web/app/src/pages/Settings.tsx"
	b, err := os.ReadFile(panel)
	if err != nil {
		t.Skipf("panel source not present: %v", err)
	}
	m := regexp.MustCompile(`const SCOPES = \[([^\]]*)\]`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("could not find the SCOPES list in %s", panel)
	}
	var got []string
	for _, part := range strings.Split(string(m[1]), ",") {
		if s := strings.Trim(strings.TrimSpace(part), `"`); s != "" {
			got = append(got, s)
		}
	}
	if strings.Join(got, ",") != strings.Join(Scopes, ",") {
		t.Errorf("the panel and auth.Scopes disagree.\n panel: %v\n   Go: %v", got, Scopes)
	}
}
