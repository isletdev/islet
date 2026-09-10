package auth

import "testing"

func TestScopeAllows(t *testing.T) {
	cases := []struct {
		scopes, method, path string
		want                 bool
	}{
		{"*", "DELETE", "/api/v1/apps/x", true},
		{"read", "GET", "/api/v1/apps", true},
		{"read", "POST", "/api/v1/apps/x/deploy", false},
		{"deploy", "POST", "/api/v1/apps/x/deploy", true},
		{"deploy", "GET", "/api/v1/cron/jobs", false},
		{"notify", "POST", "/api/v1/notify/emit", true},
		{"notify", "GET", "/api/v1/notify/channels", false},
		{"read", "GET", "/api/v1/notify/channels", true},
		{"logs", "GET", "/api/v1/docker/containers/web/logs", true},
		{"cron", "POST", "/api/v1/cron/jobs/1/run", true},
		{"read", "GET", "/api/v1/auth/tokens", false},
		{"read", "GET", "/api/v1/auth/me", true},
		{"db", "POST", "/api/v1/databases/pg/dumps", true},
		{"containers", "POST", "/api/v1/docker/containers/x/restart", true},
	}
	for _, c := range cases {
		if got := ScopeAllows(c.scopes, c.method, c.path); got != c.want {
			t.Errorf("%s %s %s: got %v want %v", c.scopes, c.method, c.path, got, c.want)
		}
	}
}
