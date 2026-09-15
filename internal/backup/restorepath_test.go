package backup

import "testing"

// Restoring used to demand the path as the container sees it — /data/paths
// followed by the host path — which appears nowhere in the panel and is not
// guessable. The obvious try, /data/<host path>, answered "path data/var: not
// found", and the one that works was found by listing the snapshot and reading
// the tree. Whatever somebody has in front of them should work.
func TestInSnapshotTakesWhatAPersonActuallyHas(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"/var/www/site", "/data/paths/var/www/site", "a host directory, which is what the plan lists"},
		{"/var/lib/islet/", "/data/paths/var/lib/islet", "a trailing slash is not a different path"},
		{"pgdata", "/data/volumes/pgdata", "a bare name is a volume, which is how volumes are named everywhere else"},
		{"/data/paths/var/www/site", "/data/paths/var/www/site", "what the snapshot listing shows is already right"},
		{"/data/volumes/pgdata", "/data/volumes/pgdata", "likewise for a volume"},
		{"/data", "/data", "the whole snapshot"},
		{"  /var/www/site  ", "/data/paths/var/www/site", "a pasted path brings whitespace with it"},
		{"", "", "nothing is still nothing, and the caller says so in words"},
	}
	for _, c := range cases {
		if got := InSnapshot(c.in); got != c.want {
			t.Errorf("InSnapshot(%q) = %q, want %q — %s", c.in, got, c.want, c.why)
		}
	}
}

// A path that climbs is flattened before it is used, so ".." cannot walk out of
// the mount it is meant to be inside.
func TestInSnapshotDoesNotLetAPathClimbOut(t *testing.T) {
	for _, in := range []string{"/var/www/../../etc/shadow", "/../etc/shadow"} {
		got := InSnapshot(in)
		if got == "/data/paths/etc/shadow" {
			continue // cleaned, and still inside the mount
		}
		if !hasPrefix(got, "/data/") {
			t.Errorf("InSnapshot(%q) = %q, which is outside the snapshot", in, got)
		}
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
