package fleet

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// The controller's identity has to be something OpenSSH will actually accept in
// authorized_keys, and something we can load back to authenticate with.
func TestKeypairRoundTrips(t *testing.T) {
	kp, err := NewKeypair("islet-panel")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(kp.PublicLine, "ssh-ed25519 ") {
		t.Errorf("public line should be an ed25519 authorized_keys entry, got %q", kp.PublicLine)
	}
	if !strings.HasSuffix(kp.PublicLine, " islet-panel") {
		t.Errorf("the comment makes the line recognisable on the server: %q", kp.PublicLine)
	}
	if strings.Count(kp.PublicLine, "\n") != 0 {
		t.Error("one line, or it corrupts authorized_keys")
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(kp.PublicLine)); err != nil {
		t.Errorf("openssh could not parse the public line: %v", err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(kp.PrivatePEM))
	if err != nil {
		t.Fatalf("could not load the private key back: %v", err)
	}
	got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if want := strings.TrimSpace(strings.TrimSuffix(kp.PublicLine, " islet-panel")); got != want {
		t.Errorf("the pair does not match:\n got %s\nwant %s", got, want)
	}
}

func TestKeypairsAreDistinct(t *testing.T) {
	a, err := NewKeypair("islet-panel")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeypair("islet-panel")
	if err != nil {
		t.Fatal(err)
	}
	if a.PrivatePEM == b.PrivatePEM {
		t.Fatal("every panel must get its own key")
	}
}

// Dial refuses before it touches the network when it has nothing to go on, so a
// person gets an answer rather than a timeout.
func TestDialNeedsACredential(t *testing.T) {
	_, err := Dial(context.Background(), "192.0.2.1:22", Credentials{User: "root"}, "")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "password or a private key") {
		t.Errorf("the message should say what is missing, got %q", err)
	}
}

func TestDialRejectsAnUnreadableKey(t *testing.T) {
	_, err := Dial(context.Background(), "192.0.2.1:22",
		Credentials{User: "root", PrivateKey: "not a key at all"}, "")
	if err == nil || !strings.Contains(err.Error(), "private key could not be read") {
		t.Errorf("expected a clear message about the key, got %v", err)
	}
}

// A key installed into authorized_keys is built by string concatenation, so a
// quote in it would break out of the shell command that writes it.
func TestInstallKeyRefusesAQuote(t *testing.T) {
	err := installKey(context.Background(), nil, "ssh-ed25519 AAAA' rm -rf /")
	if err == nil || !strings.Contains(err.Error(), "quote") {
		t.Errorf("a key containing a quote must be refused, got %v", err)
	}
}

func TestVersionNote(t *testing.T) {
	if n := versionNote("0.2.2", "0.2.2"); n != "" {
		t.Errorf("matching versions need no note, got %q", n)
	}
	if n := versionNote("v0.2.0", "v0.2.2"); !strings.Contains(n, "0.2.0") || !strings.Contains(n, "0.2.2") {
		t.Errorf("a mismatch should name both versions, got %q", n)
	}
	if n := versionNote("", "0.2.2"); n != "" {
		t.Errorf("an unknown remote version is not worth a note, got %q", n)
	}
}

func TestLastLine(t *testing.T) {
	if got := lastLine("a\nb\nislet_abc\n"); got != "islet_abc" {
		t.Errorf("the token is the last line of the output, got %q", got)
	}
	if got := lastLine("only\r\n"); got != "only" {
		t.Errorf("carriage returns come back from ssh, got %q", got)
	}
}
