package tlsutil

import "crypto/sha256"

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
