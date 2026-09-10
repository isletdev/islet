// Package version holds build information injected at link time.
package version

// Set via -ldflags "-X github.com/isletdev/islet/internal/version.Version=v0.1.0".
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
