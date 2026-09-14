package cli

import "testing"

// The released binary is isletd with a symlink named islet beside it. Getting
// this wrong does not fail loudly: the symlink starts a second daemon, which
// binds a port already in use, and every documented CLI example stops working.
func TestInvokedAsCLI(t *testing.T) {
	cases := []struct {
		argv0 string
		want  bool
	}{
		{"islet", true},
		{"./islet", true},
		{"/usr/local/bin/islet", true},
		{"islet.exe", true},
		{"isletd", false},
		{"/usr/local/bin/isletd", false},
		{"isletd.exe", false},
		{"/usr/local/bin/isletd.prev", false},
		{"", false},
	}
	for _, c := range cases {
		if got := InvokedAsCLI(c.argv0); got != c.want {
			t.Errorf("InvokedAsCLI(%q) = %v, want %v", c.argv0, got, c.want)
		}
	}
}

// Run returns a code rather than calling os.Exit, so the daemon can dispatch
// into it. These are the paths that do not need a running daemon.
func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no arguments", []string{"islet"}, 2},
		{"unknown command", []string{"islet", "nope"}, 2},
		{"help", []string{"islet", "help"}, 0},
		{"help flag", []string{"islet", "--help"}, 0},
		{"version", []string{"islet", "version"}, 0},
		{"version flag", []string{"islet", "--version"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Run(c.args); got != c.want {
				t.Errorf("Run(%q) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}
