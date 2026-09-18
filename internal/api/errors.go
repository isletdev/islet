package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/isletdev/islet/internal/ai"
	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/backup"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// What a feature package's error becomes on the wire.
//
// Twelve mappers each ended in the same line — 400 with err.Error() — and that
// one line was three problems at once.
//
// It was the wrong status for a conflict: thirteen distinct "already exists"
// errors answered 400, telling a client its request was malformed when the
// request was fine and the name was taken. It was the wrong status for a
// downstream failure: restic, psql, ufw, sshd -t, docker and the GitHub API all
// came back as "your request was bad", while dockerErr next door correctly
// answered 502 for the same class of thing. And it was a leak, because the text
// of a cmdrun error is the command line plus up to 400 bytes of its stderr —
// credentials are redacted, but paths, unit names, container names and whatever
// the tool decided to print are not.
//
// What stays is the part that was right. The hand-written messages in these
// packages are unusually good — "give it a name you will recognise later",
// "that host is already routed" — and they are written for the person who will
// read them. A blanket "internal error" would have thrown all of that away to
// fix the third of them that was never written for a human at all.

// conflict says whether an error means "that already exists", which is a 409
// rather than a 400: the request was well formed and the answer is no.
func conflict(err error) bool {
	return errors.Is(err, deploy.ErrExists) ||
		errors.Is(err, proxy.ErrExists) ||
		errors.Is(err, auth.ErrExists) ||
		errors.Is(err, ai.ErrExists) ||
		errors.Is(err, backup.ErrExists)
}

// failed is the default arm every feature mapper delegates to.
//
// kind is the error code the client sees for the ordinary case, so a mapper
// keeps its own vocabulary ("backup", "db", "invalid") while the decisions
// about status and disclosure are made in one place.
func (s *Server) failed(w http.ResponseWriter, kind string, err error) {
	switch {
	case conflict(err):
		writeJSON(w, http.StatusConflict, api.Error{Error: "exists", Message: err.Error()})
	case isCommandFailure(err):
		// The detail goes to the log, where an operator can read it, and not to
		// the client, who did not run the command and cannot fix it.
		s.logger().Warn("a command this request needed did not work", "err", err.Error())
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "downstream",
			Message: "something this needed on the server did not work. The command drawer has what ran."})
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: kind, Message: cmdrun.Redact(err.Error())})
	}
}

// isCommandFailure reports whether err came from running something.
func isCommandFailure(err error) bool {
	var ce *cmdrun.Error
	return errors.As(err, &ce)
}

// badJSON answers a request body that could not be decoded.
//
// encoding/json's message names the Go struct field it was filling — "json:
// cannot unmarshal number into Go struct field .Role of type string" — and all
// seventy-six decode sites passed it through verbatim, so the daemon described
// its own internals to anybody who sent a wrong type. The reason is logged;
// what goes back says what a client can act on.
func (s *Server) badJSON(w http.ResponseWriter, err error) {
	s.logger().Debug("a request body could not be decoded", "err", err.Error())
	writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json",
		Message: "that request body is not the JSON this endpoint expects."})
}

// logger is s.log, or the default when a Server was built without one — which
// a test does. A missing logger must not turn an error response into a panic.
func (s *Server) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
