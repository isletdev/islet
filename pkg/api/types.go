// Package api holds the request and response types of the Islet HTTP API.
// It is importable by third parties and is the source for the TypeScript
// types used by the web app.
package api

import "time"

// Health is returned by GET /api/v1/health.
// Health answers "is this daemon up", and for somebody who has signed in, "and
// which daemon is it". Everything but Status and Time is omitted for a caller
// who has not: a version and a commit are what a stranger matches against a
// list of known holes, and the hostname and server id are reconnaissance.
type Health struct {
	Status        string    `json:"status"`
	Version       string    `json:"version,omitempty"`
	Commit        string    `json:"commit,omitempty"`
	ServerID      string    `json:"serverId,omitempty"`
	Hostname      string    `json:"hostname,omitempty"`
	UptimeSeconds int64     `json:"uptimeSeconds,omitempty"`
	Time          time.Time `json:"time"`
}

// Error is the body of every non-2xx response.
type Error struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// UpdateStatus is returned by GET /api/v1/system/update.
type UpdateStatus struct {
	Current         string    `json:"current"`
	Channel         string    `json:"channel"`
	Latest          string    `json:"latest"`
	Prerelease      bool      `json:"prerelease"`
	PublishedAt     time.Time `json:"publishedAt"`
	UpdateAvailable bool      `json:"updateAvailable"`
	Notes           string    `json:"notes,omitempty"`
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	ID        int64  `json:"id"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"createdAt"`
}

// CommandEntry is one executed command, for the transparency drawer.
type CommandEntry struct {
	ID         int64  `json:"id"`
	Actor      string `json:"actor"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Stderr     string `json:"stderr,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

// Upload is a file handed to the assistant, as the panel sees it.
//
// Path is on the server and is the whole point: it is what the model is given
// and what every tool it has already takes. The panel shows it so the person
// can see where their file went.
type Upload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Type string `json:"type"`
	// Scanner is what checked this file, empty when nothing did. The panel
	// says which, because "no virus found" and "nothing looked" are different
	// answers and only one of them is reassuring.
	Scanner string `json:"scanner,omitempty"`
	AddedAt string `json:"addedAt"`
}

// Uploads is a list of them, with what this server can do about malware.
type Uploads struct {
	Files []Upload `json:"files"`
	// Scanner is the malware scanner installed here, or empty. Reported with
	// the list rather than per file so the panel can say it once, before
	// anybody uploads anything.
	Scanner string `json:"scanner,omitempty"`
	MaxSize int64  `json:"maxSize"`
}
