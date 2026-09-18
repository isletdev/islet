// Package hostown answers one question: does this daemon own the machine?
//
// Most of what Islet manages is per-daemon — its database, its workspaces, its
// apps — and a second copy running from another data directory is harmless
// because none of it is shared. Three things are not like that. The proxy is a
// single container named islet-proxy, bound to ports 80 and 443. The blocklist
// is a single ipset with iptables rules pointing at it. Both are one per
// machine, whatever Islet thinks.
//
// Running a second daemon on the same host is not exotic: it is how this
// project is developed, it is what a throwaway instance for a screenshot is,
// and it is what an operator does to try an upgrade. Until now the second one
// would take those singletons over without a word — replace the proxy container
// with one pointed at its own config, or overwrite the ipset with its own
// lists. Every site on the machine goes down, and nothing anywhere says why.
//
// So ownership is written down: the first daemon to touch a host-level resource
// claims it, by data directory, and any other daemon refuses with a message
// naming who holds it. The claim lives under /run, so a reboot clears it and
// whichever daemon starts first — normally the installed one — claims it again.
package hostown

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Path is where the claim is kept. Under /run because the answer is only true
// for as long as this boot: a machine that has restarted has no proxy container
// running and no ipset loaded, so there is nothing left to own.
const Path = "/run/islet/host-owner"

// pathFor exists so a test can point this somewhere writable. Everywhere else
// it is Path, which is the whole point of the package: one fixed location every
// daemon on the machine agrees on.
var pathFor = func() string { return Path }

// ErrNotOwner is returned when another daemon holds the machine.
var ErrNotOwner = errors.New("another Islet on this host owns it")

// Claim records dataDir as the owner, or reports who it already is.
//
// It is deliberately advisory rather than a lock. The failure this prevents is
// a second daemon quietly doing the wrong thing, not two daemons racing in the
// same millisecond — and a lock that could be held by a process that has since
// died would turn a development mistake into a machine nobody can fix.
func Claim(dataDir string) error {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	if owner, ok := Owner(); ok && owner != abs {
		return ErrNotOwner
	}
	if err := os.MkdirAll(filepath.Dir(pathFor()), 0o700); err != nil {
		return nil // no /run to write to: do not block the work over bookkeeping
	}
	_ = os.WriteFile(pathFor(), []byte(abs+"\n"), 0o600)
	return nil
}

// Owner is the data directory of the daemon that holds this machine.
func Owner() (string, bool) {
	b, err := os.ReadFile(pathFor())
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	return s, s != ""
}

// Refusal is the message a caller shows when it is not the owner. It names the
// other daemon, because "something else owns this" without saying what is the
// kind of error that costs an hour.
func Refusal(what string) error {
	owner, ok := Owner()
	if !ok {
		return nil
	}
	return errors.New("this machine's " + what + " belongs to the Islet running from " + owner +
		". Two daemons cannot share it — stop that one, or run this one with its own ports and no proxy.")
}
