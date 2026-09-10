// Command sign creates the release signing key pair and signs files with it.
// Releases ship checksums.txt plus checksums.txt.sig; the daemon verifies the
// signature with the public key embedded in internal/update before it trusts
// any checksum, so a compromised download host cannot push a bad binary.
//
//	go run ./tools/sign keygen <private-key-file>      prints the public key
//	go run ./tools/sign sign <private-key-file> <file> writes <file>.sig
//
// The private key is 64 bytes of Ed25519 seed+public, hex encoded. Keep it
// out of the repository; CI reads it from the ISLET_SIGNING_KEY secret when
// the file argument is "-".
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	switch os.Args[1] {
	case "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		check(err)
		check(os.WriteFile(os.Args[2], []byte(hex.EncodeToString(priv)+"\n"), 0o600))
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
	case "sign":
		if len(os.Args) < 4 {
			usage()
		}
		priv := loadKey(os.Args[2])
		data, err := os.ReadFile(os.Args[3])
		check(err)
		sig := ed25519.Sign(priv, data)
		check(os.WriteFile(os.Args[3]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644))
	default:
		usage()
	}
}

func loadKey(path string) ed25519.PrivateKey {
	var raw string
	if path == "-" {
		raw = os.Getenv("ISLET_SIGNING_KEY")
	} else {
		b, err := os.ReadFile(path)
		check(err)
		raw = string(b)
	}
	key, err := hex.DecodeString(strings.TrimSpace(raw))
	check(err)
	if len(key) != ed25519.PrivateKeySize {
		check(fmt.Errorf("private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(key)))
	}
	return ed25519.PrivateKey(key)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sign keygen <key-file> | sign sign <key-file|-> <file>")
	os.Exit(2)
}
