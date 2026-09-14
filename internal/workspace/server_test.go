package workspace

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// The probe has to answer from the socket alone. Asking tmux whether a server
// is running starts one when it is not — as a child of whoever asked, which is
// exactly the placement this file exists to avoid.
func TestServerUpAndStale(t *testing.T) {
	dir := t.TempDir()
	s := &Service{sock: filepath.Join(dir, "tmux.sock")}

	t.Run("no socket at all", func(t *testing.T) {
		if s.serverUp() {
			t.Error("serverUp with no socket file should be false")
		}
		if s.stale() {
			t.Error("a socket that was never created is not stale")
		}
	})

	t.Run("a server is listening", func(t *testing.T) {
		l, err := net.Listen("unix", s.sock)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		if !s.serverUp() {
			t.Error("serverUp should see a listening socket")
		}
		if s.stale() {
			t.Error("a socket with a listener is not stale")
		}
	})

	t.Run("the server is gone but the file remains", func(t *testing.T) {
		// The listener above is closed, which unlinks the socket; recreate the
		// file without anything behind it, which is what a killed tmux leaves.
		if _, err := os.Stat(s.sock); err == nil {
			_ = os.Remove(s.sock)
		}
		f, err := os.Create(s.sock)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if s.serverUp() {
			t.Error("a socket nothing answers on is not a running server")
		}
		if !s.stale() {
			t.Error("a socket file with no listener is stale and must be removed")
		}
	})
}
