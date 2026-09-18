// Package scan checks a file for malware, or says plainly that nothing did.
//
// It was written inside the assistant's uploads and moved here the moment a
// second thing needed it: a media service that takes files from the open
// internet needs the check far more than a panel that takes them from the
// person who owns the server.
//
// The policy is the same in both places and is worth stating once. ClamAV wants
// about a gigabyte of RAM for its signatures and this daemon is meant to run on
// a server with one, so requiring it would make these features dead on exactly
// the machines Islet is for. It is used when it is there, said out loud when it
// is not, and a scanner that is installed and cannot answer refuses — because a
// broken clamd that quietly passes everything is worse than one that stops the
// uploads.
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
)

// Infected is a file a scanner objected to. It carries what the scanner said,
// because "rejected" without the signature name is not something anyone can act
// on.
type Infected struct{ Signature string }

func (e *Infected) Error() string {
	if e.Signature == "" {
		return "the virus scanner rejected this file"
	}
	return "the virus scanner rejected this file: " + e.Signature
}

// Scanner runs the malware check this machine has.
type Scanner struct {
	cmds *cmdrun.Runner
	log  *slog.Logger
	// Timeout bounds one scan. A zip of a brand kit is a few thousand files and
	// clamscan is not fast; a video is one file and large.
	Timeout time.Duration
	// look is the lookup, replaced in tests so the refusal paths can be driven
	// on a machine with no scanner installed — which is most machines.
	look func(ctx context.Context) string
}

func New(cmds *cmdrun.Runner, log *slog.Logger) *Scanner {
	if log == nil {
		log = slog.Default()
	}
	s := &Scanner{cmds: cmds, log: log, Timeout: 3 * time.Minute}
	s.look = s.find
	return s
}

// Name is the scanner installed here, or empty.
//
// clamdscan first: it asks a running daemon that already holds the signatures,
// which is the difference between a second and half a minute. clamscan loads
// them itself every time, which is slow but works on a machine where nobody
// wanted a resident daemon on a gigabyte of RAM.
func (s *Scanner) Name(ctx context.Context) string { return s.look(ctx) }

func (s *Scanner) find(ctx context.Context) string {
	if s == nil || s.cmds == nil {
		return ""
	}
	for _, name := range []string{"clamdscan", "clamscan"} {
		if _, err := s.cmds.Read(ctx, "sh", "-c", "command -v "+name); err == nil {
			return name
		}
	}
	return ""
}

// File checks one path. The name of whatever looked comes back, empty when
// nothing did — a fact for the caller to pass on rather than a failure.
func (s *Scanner) File(ctx context.Context, actor, path string) (string, error) {
	if s == nil || s.cmds == nil {
		return "", nil
	}
	name := s.Name(ctx)
	if name == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	args := []string{"--no-summary", path}
	if name == "clamdscan" {
		// --fdpass hands the open descriptor over, so the daemon reads a file
		// it would otherwise have no permission to open: these live under the
		// data directory, which is Islet's and not clamav's.
		args = []string{"--no-summary", "--fdpass", path}
	}
	res, err := s.cmds.Run(ctx, actor, name, args...)
	switch {
	case err == nil:
		return name, nil
	case res.ExitCode == 1:
		return "", &Infected{Signature: signature(res.Stdout)}
	default:
		out := strings.TrimSpace(res.Stderr)
		if out == "" {
			out = strings.TrimSpace(res.Stdout)
		}
		if out == "" {
			out = err.Error()
		}
		s.log.Warn("the virus scanner could not check a file", "scanner", name, "err", out)
		return "", fmt.Errorf("%s could not check this file: %s", name, firstLine(out))
	}
}

// signature pulls the name out of "…/file: Eicar-Signature FOUND".
func signature(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.LastIndex(line, ": "); i >= 0 && strings.HasSuffix(strings.TrimSpace(line), "FOUND") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[i+2:]), "FOUND"))
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
