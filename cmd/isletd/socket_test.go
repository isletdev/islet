package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// /run/islet/isletd.sock is one fixed path, and startup removes a stale socket
// there while shutdown removes its own. A development daemon beside the
// installed one — how this project is worked on — therefore took the installed
// daemon's socket away twice: once starting, once stopping.
//
// What is asserted here is the decision, not the filesystem. socketPath falls
// back to the data directory when /run cannot be created, which is correct and
// is what happens for an unprivileged process; an earlier version of this test
// asserted the /run outcome unconditionally, passed as root and failed in CI.
func TestADaemonWithItsOwnDataDirectoryKeepsItsSocketThere(t *testing.T) {
	unsetSocketEnv(t)

	dev := t.TempDir()
	got := socketPath(dev)
	if got != filepath.Join(dev, "isletd.sock") {
		t.Errorf("a daemon with its own data directory should keep its socket there, got %q", got)
	}
	if strings.HasPrefix(got, "/run/") {
		t.Errorf("only the installed daemon may use /run, got %q", got)
	}
	// The property the bug was about: two daemons, two sockets.
	if socketPath(dev) == socketPath(t.TempDir()) {
		t.Error("two data directories must not share one socket path")
	}
	if socketPath(dev) == "/run/islet/isletd.sock" {
		t.Error("a development daemon must never land on the installed path")
	}
}

// The installed daemon uses /run when it can. It cannot as an unprivileged
// process, and falling back to the data directory is the right answer then, so
// this only asserts the /run case when /run/islet is actually creatable.
func TestTheInstalledDaemonUsesRunWhenItCan(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the /run path is Linux only")
	}
	unsetSocketEnv(t)
	if err := os.MkdirAll("/run/islet", 0o700); err != nil {
		t.Skipf("/run/islet is not creatable here: %v", err)
	}
	if got := socketPath(defaultDataDir()); got != "/run/islet/isletd.sock" {
		t.Errorf("the installed daemon should use /run, got %q", got)
	}
}

// An explicit setting still wins, including the empty value that turns the
// socket off.
func TestExplicitSocketPathWins(t *testing.T) {
	t.Setenv("ISLET_SOCKET", "/tmp/somewhere.sock")
	if got := socketPath("/var/lib/islet"); got != "/tmp/somewhere.sock" {
		t.Errorf("got %q", got)
	}
	t.Setenv("ISLET_SOCKET", "")
	if got := socketPath("/var/lib/islet"); got != "" {
		t.Errorf("an empty ISLET_SOCKET disables the socket, got %q", got)
	}
}

// unsetSocketEnv removes the variable rather than emptying it: empty is the
// documented way to turn the socket off, which is a different thing.
func unsetSocketEnv(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("ISLET_SOCKET")
	if had {
		t.Cleanup(func() { _ = os.Setenv("ISLET_SOCKET", old) })
	}
	if err := os.Unsetenv("ISLET_SOCKET"); err != nil {
		t.Fatal(err)
	}
}
