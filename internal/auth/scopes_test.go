package auth

import "testing"

// A token minted read-only used to satisfy the host terminal and container
// exec, because both start as a GET and the scope table ended in "any GET is a
// read". That turned a monitoring token into a root shell.
func TestReadScopeCannotOpenAShell(t *testing.T) {
	for _, path := range []string{
		"/api/v1/terminal/ws",
		"/api/v1/docker/containers/abc/exec",
	} {
		if ScopeAllows("read", "GET", path) {
			t.Errorf("read scope must not reach %s", path)
		}
		if !ScopeAllows("shell", "GET", path) {
			t.Errorf("shell scope should reach %s", path)
		}
	}
}

// A path nobody classified must not inherit the read scope.
func TestUnknownPathsAreDenied(t *testing.T) {
	for _, path := range []string{"/api/v1/something-new", "/api/v1/admin/danger"} {
		if ScopeAllows("read", "GET", path) {
			t.Errorf("unknown path %s must deny by default", path)
		}
	}
}

func TestReadScopeStillReadsWhatItShould(t *testing.T) {
	for _, path := range []string{
		"/api/v1/system",
		"/api/v1/metrics/latest",
		"/api/v1/domains",
		"/api/v1/security",
		"/api/v1/backups",
		"/api/v1/apps",
		"/api/v1/docker/containers",
	} {
		if !ScopeAllows("read", "GET", path) {
			t.Errorf("read scope should reach %s", path)
		}
	}
	if ScopeAllows("read", "POST", "/api/v1/apps/x/deploy") {
		t.Error("read scope must not deploy")
	}
}

// A token is its owner acting later, so it cannot carry more than the owner has.
func TestCapScopes(t *testing.T) {
	if _, err := CapScopes("deploy", "viewer"); err == nil {
		t.Error("a viewer must not mint a deploy token")
	}
	if _, err := CapScopes("shell", "deployer"); err == nil {
		t.Error("only an admin may mint a shell token")
	}
	if got, err := CapScopes("shell", "admin"); err != nil || got != "shell" {
		t.Errorf("admin shell token = %q, %v", got, err)
	}
	// An empty request means "everything this role may hold", not everything.
	got, err := CapScopes("", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if !ScopeAllows(got, "GET", "/api/v1/system") {
		t.Errorf("a viewer's default token should still read: %q", got)
	}
	for _, bad := range []string{"deploy", "cron", "containers", "shell", "db"} {
		if ScopeAllows(got, "POST", "/api/v1/apps/x/deploy") {
			t.Fatalf("viewer default token %q can write", got)
		}
		_ = bad
	}
	if _, err := CapScopes("read,deploy", "deployer"); err != nil {
		t.Errorf("a deployer may hold deploy: %v", err)
	}
}
