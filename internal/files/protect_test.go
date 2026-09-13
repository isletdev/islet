package files

import "testing"

// The guard compared the raw text of the path, so a doubled slash slipped past
// while the reader that followed cleaned it to exactly the protected path. Any
// signed-in user could read the daemon's own key directory that way.
func TestIsProtectedNormalisesFirst(t *testing.T) {
	protected := []string{
		"/etc/shadow",
		"/etc//shadow",
		"/etc/./shadow",
		"/etc/ssh/../ssh/sshd_config",
		"/var/lib/islet//secret.key",
		"/var/lib/islet/islet.db",
		"/var/lib/./islet/islet.db",
		"/root",
		"/root/.bashrc",
		"/home/lev/.ssh/id_rsa",
		"/home/lev/.ssh",
		"/var/log/auth.log",
		"/home/lev/.aws/credentials",
	}
	for _, p := range protected {
		if !IsProtected(p) {
			t.Errorf("%s must be protected", p)
		}
	}

	open := []string{
		"/srv/app",
		"/home/lev/notes.txt",
		"/var/log/nginx/access.log",
		"/etc/hosts",
		"/tmp",
	}
	for _, p := range open {
		if IsProtected(p) {
			t.Errorf("%s should not be protected", p)
		}
	}
}
