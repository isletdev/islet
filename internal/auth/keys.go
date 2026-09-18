package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Keys wraps the daemon's secret key, stored once in the data directory with
// mode 0600 and used to encrypt secrets at rest.
type Keys struct {
	aead cipher.AEAD
	// id names the key that sealed a value, so a future rotation can tell an
	// old ciphertext from a new one. See Encrypt.
	id [keyIDLen]byte
}

// The stored form of a sealed value.
//
// It used to be nonce||ciphertext and nothing else, which meant a blob carried
// no record of what sealed it. That is what makes rotation impossible rather
// than merely unwritten: with two keys in play there is no way to tell which
// one opens which row, so rotating means decrypting everything with the old key
// and re-encrypting in one pass that cannot be interrupted, and a disclosed key
// means re-entering every credential by hand.
//
// So a sealed value now starts with a marker and four bytes naming the key. The
// marker is deliberately not a plausible prefix for the old format's random
// nonce, and a mismatch falls back to reading the value the old way, so every
// value written before this change still opens. Nothing has to be migrated: the
// next save of any secret writes the new form.
var blobMagic = [3]byte{0x49, 0x4b, 0x01} // "IK", version 1

const keyIDLen = 4

// LoadOrCreateKeys reads <dataDir>/secret.key or creates it.
func LoadOrCreateKeys(dataDir string) (*Keys, error) {
	path := filepath.Join(dataDir, "secret.key")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		raw = []byte(hex.EncodeToString(key) + "\n")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, fmt.Errorf("write secret key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read secret key: %w", err)
	}
	key, err := hex.DecodeString(trimNL(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("secret key at %s is malformed", path)
	}
	// Derive the encryption key so the file's raw bytes are never used directly.
	sum := sha256.Sum256(append([]byte("islet.enc.v1:"), key...))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	k := &Keys{aead: aead}
	// The id is derived from the key and is not a secret: it says which key,
	// not what it is. A separate domain string keeps it independent of the
	// encryption key derived above.
	idSum := sha256.Sum256(append([]byte("islet.keyid.v1:"), key...))
	copy(k.id[:], idSum[:keyIDLen])
	return k, nil
}

// ID is the four bytes naming this key, as hex. It identifies which key sealed
// a value, which is the thing rotation needs and the thing that was missing.
func (k *Keys) ID() string { return hex.EncodeToString(k.id[:]) }

// SealedBy reports whether this key is the one that sealed blob. A value in the
// old format answers true, because there was only ever one key then.
func (k *Keys) SealedBy(blob []byte) bool {
	id, _, ok := splitBlob(blob)
	return !ok || id == k.id
}

// splitBlob separates the key id from the sealed bytes. ok is false for a value
// written before the marker existed.
func splitBlob(blob []byte) (id [keyIDLen]byte, rest []byte, ok bool) {
	if len(blob) < len(blobMagic)+keyIDLen || [3]byte(blob[:3]) != blobMagic {
		return id, blob, false
	}
	copy(id[:], blob[len(blobMagic):len(blobMagic)+keyIDLen])
	return id, blob[len(blobMagic)+keyIDLen:], true
}

// Encrypt seals plaintext: marker, key id, random nonce, ciphertext.
func (k *Keys) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(blobMagic)+keyIDLen+len(nonce)+len(plaintext)+k.aead.Overhead())
	out = append(out, blobMagic[:]...)
	out = append(out, k.id[:]...)
	out = append(out, nonce...)
	return k.aead.Seal(out, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, and still opens a value written before the marker
// existed — every secret stored by a daemon older than this.
func (k *Keys) Decrypt(blob []byte) ([]byte, error) {
	id, rest, tagged := splitBlob(blob)
	if tagged && id != k.id {
		return nil, errors.New("this value was sealed with a different key")
	}
	n := k.aead.NonceSize()
	if len(rest) < n {
		return nil, errors.New("ciphertext too short")
	}
	out, err := k.aead.Open(nil, rest[:n], rest[n:], nil)
	if err == nil || tagged {
		return out, err
	}
	// An untagged read that failed may be a tagged value whose marker collided
	// with a random nonce, which is roughly one in sixteen million and still
	// worth not corrupting.
	if _, rest2, ok := splitBlob(blob); ok && len(rest2) >= n {
		return k.aead.Open(nil, rest2[:n], rest2[n:], nil)
	}
	return nil, err
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
