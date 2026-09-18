package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
)

var routeLine = regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+) (/[^"]*)"(.*)`)

// registeredRoutes reads the route table the way the mux does, from the source,
// so the test cannot drift by being updated alongside the thing it checks.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range []string{"server.go", "sqlclient.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("cannot read %s: %v", f, err)
		}
		for _, m := range routeLine.FindAllStringSubmatch(string(b), -1) {
			method, path, tail := m[1], m[2], m[3]
			// A route with no session has no role to check: webhooks carry
			// their own signature, the heartbeat URL is the secret, setup runs
			// once before anybody exists.
			if !strings.Contains(tail, "requireAuth") {
				continue
			}
			out[method+" "+path] = true
		}
	}
	return out
}

// Every authenticated route has to say which role it needs.
//
// This is the test that makes the table in roles.go worth having. Without it
// the table is documentation, and a route added without a line would be refused
// at runtime by the default — correct, but discovered by a person hitting a 403
// on a feature that ought to work. With it, forgetting is a red build.
func TestEveryRouteDeclaresARole(t *testing.T) {
	for route := range registeredRoutes(t) {
		if _, ok := minRole[route]; !ok {
			t.Errorf("%s has no entry in minRole; add one to internal/api/roles.go", route)
		}
	}
}

// And the table may not name a route that is not there. A stale line is not
// harmless: it reads as a deliberate decision about a route, and the next
// person to reorganise the API will believe it.
func TestTheRoleTableHasNoStaleEntries(t *testing.T) {
	routes := registeredRoutes(t)
	for route := range minRole {
		if !routes[route] {
			t.Errorf("minRole names %q, which is not registered", route)
		}
	}
}

// Every role named has to be one the ranking knows. A typo here would be a
// route nobody can reach, since an unknown role ranks zero.
func TestTheRoleTableNamesRealRoles(t *testing.T) {
	for route, role := range minRole {
		if roleRank[role] == 0 {
			t.Errorf("%s wants role %q, which does not exist", route, role)
		}
	}
}

// The three findings that made this table necessary, written down as tests so
// they cannot come back quietly. Each of these routes had no check of any kind:
// anybody signed in could write and delete the credentials that get expanded
// into app environments, or send a notification to every configured channel.
func TestTheRoutesThatHadNoCheckNowHaveOne(t *testing.T) {
	for _, tc := range []struct{ route, want string }{
		{"POST /api/v1/vault", admin},
		{"DELETE /api/v1/vault/{name}", admin},
		{"POST /api/v1/notify/emit", deployer},
	} {
		if got := minRole[tc.route]; got != tc.want {
			t.Errorf("%s needs %s, table says %q", tc.route, tc.want, got)
		}
	}
}

// A viewer must not be able to reach anything that writes. Self-service is the
// exception and is listed rather than pattern-matched, because "does this write
// something the person owns" is a judgement and judgements belong in writing.
func TestAViewerReachesNothingThatWritesSomebodyElsesState(t *testing.T) {
	selfService := map[string]bool{
		"POST /api/v1/auth/password":              true, // their own password
		"POST /api/v1/auth/totp/setup":            true, // their own second factor
		"POST /api/v1/auth/totp/enable":           true,
		"POST /api/v1/auth/totp/disable":          true,
		"POST /api/v1/auth/tokens":                true, // capped to their own scopes at mint
		"DELETE /api/v1/auth/tokens/{id}":         true,
		"DELETE /api/v1/auth/sessions/{id}":       true,
		"POST /api/v1/assistant/chat":             true, // acts with their authority, not more
		"POST /api/v1/assistant/chats":            true,
		"POST /api/v1/assistant/chats/{id}":       true,
		"DELETE /api/v1/assistant/chats/{id}":     true,
		"POST /api/v1/assistant/runs/{id}/cancel": true,
		"POST /api/v1/cron/preview":               true, // pure functions over the body
		"POST /api/v1/cron/lint":                  true,
	}
	for route, role := range minRole {
		if role != viewer || strings.HasPrefix(route, "GET ") || selfService[route] {
			continue
		}
		t.Errorf("%s is reachable by a viewer and is not read-only or self-service", route)
	}
}

// The mechanism, not just the table: a request has to carry its pattern this
// far for any of the above to mean anything. It does, because the mux sets it
// while routing and requireAuth runs after that — but it is one field on a
// struct in the standard library, so it is worth a test that would notice if it
// ever arrived empty and quietly sent every route to the admin default.
func TestTheRoleFloorReadsTheRoutingPattern(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/vault/{name}", func(w http.ResponseWriter, r *http.Request) {
		if s.enforceRole(w, r) {
			w.WriteHeader(http.StatusNoContent)
		}
	})

	for _, tc := range []struct {
		role string
		want int
	}{
		{"viewer", http.StatusForbidden},
		{"deployer", http.StatusForbidden},
		{"admin", http.StatusNoContent},
	} {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/vault/stripe-key", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxUser, &auth.User{Role: tc.role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("a %s deleting a secret got %d, want %d: %s", tc.role, w.Code, tc.want, w.Body)
		}
	}
}

// An unlisted pattern is refused rather than waved through. This is the half of
// the design that makes forgetting safe.
func TestAnUnlistedRouteIsAdminOnly(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/something-nobody-classified", func(w http.ResponseWriter, r *http.Request) {
		if s.enforceRole(w, r) {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something-nobody-classified", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxUser, &auth.User{Role: "deployer"}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("an unclassified route let a deployer through with %d", w.Code)
	}
}
