package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// /run/islet/isletd.sock is one fixed path, and startup removes a stale socket
// there while shutdown removes its own. A development daemon beside the
// installed one — how this project is worked on — therefore took the installed
// daemon's socket away twice: once starting, once stopping.
func TestOnlyTheInstalledDaemonUsesRun(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the /run path is Linux only")
	}
	// An unset variable, not an empty one: empty is the documented way to
	// turn the socket off, which is a different thing.
	if err := os.Unsetenv("ISLET_SOCKET"); err != nil {
		t.Fatal(err)
	}

	if got := socketPath(defaultDataDir()); got != "/run/islet/isletd.sock" {
		t.Errorf("the installed daemon should use /run, got %q", got)
	}
	dev := t.TempDir()
	if got := socketPath(dev); got != filepath.Join(dev, "isletd.sock") {
		t.Errorf("a daemon with its own data directory should keep its socket there, got %q", got)
	}
	if socketPath(dev) == socketPath(defaultDataDir()) {
		t.Error("two daemons must not share one socket path")
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
		t.Errorf("an empty ISLET_SOCKET disables it, got %q", got)
	}
}
