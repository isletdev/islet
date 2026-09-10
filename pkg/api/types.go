// Package api holds the request and response types of the Islet HTTP API.
// It is importable by third parties and is the source for the TypeScript
// types used by the web app.
package api

import "time"

// Health is returned by GET /api/v1/health.
type Health struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	Commit        string    `json:"commit"`
	ServerID      string    `json:"serverId"`
	Hostname      string    `json:"hostname"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
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
