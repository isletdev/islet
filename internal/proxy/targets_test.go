package proxy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/store"
)

// Routing to something on this machine is ordinary: a service on a port,
// reached through the host gateway, is how most url targets are written. The
// check added here must not get in the way of that.
func TestAnOrdinaryURLTargetIsStillAccepted(t *testing.T) {
	for _, target := range []string{
		"http://localhost:3000",
		"http://127.0.0.1:8080",
		"https://api.example.com",
		"http://host.docker.internal:5000",
		"http://10.1.2.3:9000",
	} {
		if err := validateURLTarget(target); err != nil {
			t.Errorf("%s was refused: %v", target, err)
		}
	}
}

// Link-local has no innocent reading. 169.254.169.254 is the cloud metadata
// endpoint on every major provider: instance credentials, no authentication,
// trusted precisely because only something on the machine can reach it.
// Publishing it at a hostname hands those credentials to the internet.
func TestTheMetadataAddressCannotBePublished(t *testing.T) {
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.170.2/v2/credentials",
		"http://[fe80::1]/",
	} {
		err := validateURLTarget(target)
		if err == nil {
			t.Errorf("%s was accepted as a site backend", target)
			continue
		}
		if !strings.Contains(err.Error(), "link-local") {
			t.Errorf("%s was refused for the wrong reason: %v", target, err)
		}
	}
}

// And a URL has to be a URL. The whole check used to be a prefix test, which
// accepted anything at all after the scheme.
func TestAURLTargetHasToParse(t *testing.T) {
	for _, target := range []string{"", "not a url", "ftp://example.com", "http://"} {
		if err := validateURLTarget(target); err == nil {
			t.Errorf("%q was accepted", target)
		}
	}
}

// The panel's own address, written as a url target, published the panel past
// the admin-only check on the panel target type and past the IP restriction.
func TestAURLTargetCannotBeThePanel(t *testing.T) {
	const panel = "https://host.docker.internal:9443"
	for _, target := range []string{
		"https://host.docker.internal:9443",
		"https://127.0.0.1:9443",
		"http://localhost:9443/",
		"https://[::1]:9443",
	} {
		if !pointsAtPanel("url", target, panel) {
			t.Errorf("%s was not recognised as the panel", target)
		}
	}
	// A different port on the same host is somebody's app, not the panel.
	for _, target := range []string{"http://localhost:3000", "https://127.0.0.1:8443", "https://example.com:9443"} {
		if pointsAtPanel("url", target, panel) {
			t.Errorf("%s was mistaken for the panel", target)
		}
	}
	// And a container that happens to be named like one is not a url target.
	if pointsAtPanel("container", "localhost", panel) {
		t.Error("a container target was treated as a url")
	}
}

// Editing a domain must not delete its basic-auth users.
//
// The editor opens with the field blank, because it holds bcrypt hashes and
// showing them would be worse. The save path took that blank literally, so
// opening a site to change its port and pressing Save removed everybody who
// could sign in to it, silently.
func TestSavingADomainKeepsItsBasicAuthUsers(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m := New(nil, st, t.TempDir(), "")

	d := &Domain{Host: "app.example.com", TargetType: "url", Target: "http://127.0.0.1:3000", TLS: "none", Enabled: true, BasicAuth: "alice:hunter2"}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := m.writeDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	stored, err := m.Domain(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.BasicAuth, "alice:$2") {
		t.Fatalf("the password was not hashed on the way in: %q", stored.BasicAuth)
	}
	hashed := stored.BasicAuth

	// What the panel sends when somebody edits the port: everything it read
	// back, with the credential field blank.
	edit := *stored
	edit.BasicAuth = ""
	edit.Port = 4000
	if err := edit.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := m.writeDomain(ctx, &edit); err != nil {
		t.Fatal(err)
	}
	after, err := m.Domain(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.BasicAuth != hashed {
		t.Errorf("the users were lost on an unrelated save: %q", after.BasicAuth)
	}

	// Removing them is a thing somebody can still do, but it has to be said.
	clear := *after
	clear.BasicAuth, clear.ClearBasicAuth = "", true
	if err := clear.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := m.writeDomain(ctx, &clear); err != nil {
		t.Fatal(err)
	}
	gone, err := m.Domain(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gone.BasicAuth != "" {
		t.Errorf("an explicit removal left %q behind", gone.BasicAuth)
	}
}
