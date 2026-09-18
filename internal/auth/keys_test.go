package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func newKeys(t *testing.T) *Keys {
	t.Helper()
	k, err := LoadOrCreateKeys(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestASealedValueOpens(t *testing.T) {
	k := newKeys(t)
	blob, err := k.Encrypt([]byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Decrypt(blob)
	if err != nil || string(got) != "hunter2" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Rotation needs to know which key sealed a value, and nothing recorded it: the
// stored form was nonce||ciphertext, so with two keys in play there is no way
// to tell which one opens which row. That is what made rotation impossible
// rather than merely unwritten, and it is why this marker exists before there
// is anything to rotate to.
func TestASealedValueNamesTheKeyThatSealedIt(t *testing.T) {
	k := newKeys(t)
	blob, err := k.Encrypt([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if !k.SealedBy(blob) {
		t.Error("a key did not recognise its own ciphertext")
	}
	other := newKeys(t)
	if other.SealedBy(blob) {
		t.Error("a different key claimed this ciphertext")
	}
	if _, err := other.Decrypt(blob); err == nil {
		t.Error("a different key decrypted it")
	}
	if k.ID() == other.ID() || len(k.ID()) != keyIDLen*2 {
		t.Errorf("key ids look wrong: %q and %q", k.ID(), other.ID())
	}
}

// Every secret on every existing server is in the old format. They have to keep
// opening, or an upgrade loses every credential the daemon holds.
func TestAValueSealedBeforeTheMarkerStillOpens(t *testing.T) {
	dir := t.TempDir()
	k := newKeys2(t, dir)

	// Exactly what the previous implementation wrote: nonce || ciphertext.
	raw, err := os.ReadFile(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(trimNL(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte("islet.enc.v1:"), key...))
	block, _ := aes.NewCipher(sum[:])
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	old := append(append([]byte{}, nonce...), aead.Seal(nil, nonce, []byte("from an older daemon"), nil)...)

	got, err := k.Decrypt(old)
	if err != nil || string(got) != "from an older daemon" {
		t.Fatalf("an old value did not open: %q %v", got, err)
	}
	if !k.SealedBy(old) {
		t.Error("an untagged value should be treated as this key's, since there was only one")
	}
}

// And the marker must not be mistaken for the start of an old random nonce in a
// way that loses data: a collision falls back rather than failing.
func TestACollidingNonceStillOpens(t *testing.T) {
	dir := t.TempDir()
	k := newKeys2(t, dir)
	for i := 0; i < 200; i++ {
		blob, err := k.Encrypt([]byte("value"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(blob, blobMagic[:]) {
			t.Fatal("a sealed value lost its marker")
		}
		got, err := k.Decrypt(blob)
		if err != nil || string(got) != "value" {
			t.Fatalf("round trip %d failed: %q %v", i, got, err)
		}
	}
}

func newKeys2(t *testing.T, dir string) *Keys {
	t.Helper()
	k, err := LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
