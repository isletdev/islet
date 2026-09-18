package api

import (
	"testing"

	"github.com/isletdev/islet/internal/auth"
)

// The scope rules match on a path, and the fleet proxy forwards one path under
// another. Checking only the local path meant that prefixing
// /api/v1/servers/{id}/proxy/ turned every deliberate carve-out into nothing:
// the rule saw a path in the "servers" area and answered for that.
//
// This test states the carve-outs as they have to hold on a managed server,
// which is where they matter most — a token that cannot open a terminal here
// must not open one there either.
func TestScopesMeanTheSameThingThroughTheFleetProxy(t *testing.T) {
	for _, tc := range []struct {
		name, scopes, method, remote string
		want                         bool
	}{
		// A terminal is a shell. It is a GET only because that is how a
		// WebSocket starts, which is exactly why it has a rule of its own.
		{"a terminal needs shell", "system", "GET", "/api/v1/terminal/ws", false},
		{"and shell still opens one", "shell", "GET", "/api/v1/terminal/ws", true},

		// Revealing a secret is denied to every scope: no token reads a vault
		// value, on any machine.
		{"no scope reveals a secret", "system", "POST", "/api/v1/vault/stripe/reveal", false},
		{"not even read", "read", "POST", "/api/v1/vault/stripe/reveal", false},

		// No scope mints a token, because a narrow scope that could mint would
		// not be narrow.
		{"no scope mints a token", "system", "POST", "/api/v1/auth/tokens", false},

		// Files need the files scope, and read is not it.
		{"reading a file needs files", "read", "GET", "/api/v1/files/read", false},
		{"files reads one", "files", "GET", "/api/v1/files/read", true},

		// What should still work, so the check is a gate and not a wall.
		{"read reads domains", "read", "GET", "/api/v1/domains", true},
		{"containers acts on a container", "containers", "POST", "/api/v1/docker/containers/x/restart", true},
	} {
		// The path as the proxy forwards it: this is what the handler now
		// checks, in addition to the route the caller actually asked for.
		if got := auth.ScopeAllows(tc.scopes, tc.method, tc.remote); got != tc.want {
			t.Errorf("%s: ScopeAllows(%q, %s %s) = %v, want %v", tc.name, tc.scopes, tc.method, tc.remote, got, tc.want)
		}
	}
}

// And the local route these travel under is, on its own, wide open to anything
// holding "system" — which is the whole reason the remote path has to be
// checked separately rather than trusted to the rule that let the request in.
func TestTheProxyRouteItselfLooksHarmlessToTheScopeRules(t *testing.T) {
	const local = "/api/v1/servers/abc/proxy/terminal/ws"
	if !auth.ScopeAllows("system", "GET", local) {
		t.Skip("the servers area no longer admits system; this test's premise is gone")
	}
	if auth.ScopeAllows("system", "GET", "/api/v1/terminal/ws") {
		t.Error("a system token can now open a terminal locally, which would make the proxy check pointless rather than load-bearing")
	}
}
