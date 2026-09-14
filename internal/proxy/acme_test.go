package proxy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Traefik refuses to load an ACME account from a file more permissive than
// 0600, and its response is to drop the resolver and carry on serving its own
// self-signed certificate for every site. Nothing about that is loud, so the
// only defence is never to hand it a file it will refuse.
func TestEnsureACMEFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.json")

	if err := ensureACMEFile(path); err != nil {
		t.Fatalf("create: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "{}" {
		t.Fatalf("content = %q, %v", b, err)
	}

	// An existing store keeps its contents.
	if err := os.WriteFile(path, []byte(`{"letsencrypt":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureACMEFile(path); err != nil {
		t.Fatalf("existing: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"letsencrypt":{}}` {
		t.Fatalf("the certificate store was overwritten: %q", b)
	}

	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced here")
	}
	// The case that broke it: a store that outlived the install that made it,
	// widened by a restore, a copy, or an older version.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureACMEFile(path); err != nil {
		t.Fatalf("repair: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600; Traefik would refuse this file", info.Mode().Perm())
	}
}

// The words a person reads have to agree with how many hosts there are.
func TestHostListAndVerb(t *testing.T) {
	for _, tc := range []struct {
		hosts []string
		want  string
	}{
		{[]string{"a.example.com"}, "a.example.com"},
		{[]string{"a.example.com", "b.example.com"}, "a.example.com and b.example.com"},
		{[]string{"a.example.com", "b.example.com", "c.example.com"}, "a.example.com, b.example.com and c.example.com"},
		{[]string{"a", "b", "c", "d"}, "a, b and 2 others"},
	} {
		if got := hostList(tc.hosts); got != tc.want {
			t.Errorf("hostList(%v) = %q, want %q", tc.hosts, got, tc.want)
		}
	}
	if verb(1, "asks for", "ask for") != "asks for" || verb(2, "asks for", "ask for") != "ask for" {
		t.Error("verb does not agree with the number")
	}
}
