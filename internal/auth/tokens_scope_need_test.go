package auth

import "testing"

// A refusal names what would have let the call through, and says so plainly
// when nothing would.
func TestScopeNeeded(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"POST", "/api/v1/workspaces", "workspaces"},
		{"GET", "/api/v1/workspaces", "read"},
		{"POST", "/api/v1/files/op", "files"},
		{"GET", "/api/v1/auth/tokens", ""},
	} {
		if got := ScopeNeeded(c.method, c.path); got != c.want {
			t.Errorf("%s %s needs %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
