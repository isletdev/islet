package proxy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/store"
)

// Replacing the proxy has to finish even when the request that asked for it is
// gone.
//
// Through the panel's own domain the request travels through the proxy being
// replaced, so `docker stop` closes it and its context is cancelled. Install
// used to run every later command on that context, so none of them started:
// not the new container, not the rollback. The machine was left with nothing
// on 80 and 443 and the old proxy stopped under another name — a 522 on every
// site behind Cloudflare, and no panel at the address it was asked from.
//
// The test hands Install a context that is already cancelled, which is the
// state it was in from `docker stop` onwards, and a docker that answers like a
// running proxy and records what it was asked.
func TestInstallSurvivesTheCallerGoing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as docker")
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := `#!/bin/sh
echo "$*" >> "` + calls + `"
case "$*" in
  *"{{.State.Running}} {{.Config.Image}}"*) echo "true ` + Image + `" ;;
  *"{{.State.Running}}"*) echo "true" ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := claimHost
	claimHost = func(string) error { return nil }
	t.Cleanup(func() { claimHost = old })

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m := New(cmdrun.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), st, t.TempDir(), "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Install(ctx, "test", "ops@example.com"); err != nil {
		t.Fatalf("Install with the caller gone: %v", err)
	}

	b, _ := os.ReadFile(calls)
	log := string(b)
	for _, want := range []string{"stop " + ContainerName, "rename " + ContainerName + " " + ContainerName + "-prev", "run -d --name " + ContainerName} {
		if !strings.Contains(log, want) {
			t.Errorf("docker was never asked to %q; it was asked:\n%s", want, log)
		}
	}
	if strings.Contains(log, "rename "+ContainerName+"-prev "+ContainerName) {
		t.Errorf("the old proxy was put back, so the new one was refused:\n%s", log)
	}
}
