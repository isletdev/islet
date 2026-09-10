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
// mode 0600 and used to encrypt secrets at rest (TOTP seeds now, credentials
// for integrations later).
type Keys struct {
	aead cipher.AEAD
}

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
	return &Keys{aead: aead}, nil
}

// Encrypt seals plaintext with a random nonce prefixed to the output.
func (k *Keys) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(nonce, k.aead.Seal(nil, nonce, plaintext, nil)...), nil
}

// Decrypt reverses Encrypt.
func (k *Keys) Decrypt(blob []byte) ([]byte, error) {
	n := k.aead.NonceSize()
	if len(blob) < n {
		return nil, errors.New("ciphertext too short")
	}
	return k.aead.Open(nil, blob[:n], blob[n:], nil)
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
