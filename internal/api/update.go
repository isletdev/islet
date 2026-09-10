package api

import (
	"net/http"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/isletdev/islet/internal/update"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

func channelFrom(r *http.Request) update.Channel {
	if r.URL.Query().Get("channel") == "beta" {
		return update.Beta
	}
	return update.Stable
}

// handleUpdateCheck answers whether a newer release exists.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ch := channelFrom(r)
	rel, err := update.Latest(r.Context(), ch)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "upstream", Message: "could not reach GitHub releases: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, api.UpdateStatus{
		Current:         version.Version,
		Channel:         string(ch),
		Latest:          rel.Version,
		Prerelease:      rel.Prerelease,
		PublishedAt:     rel.PublishedAt,
		UpdateAvailable: update.IsNewer(rel.Version, version.Version),
		Notes:           rel.Notes,
	})
}

// handleUpdateApply installs the latest release and restarts the daemon.
// systemd's Restart=always brings the new binary up.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can update"})
		return
	}
	ch := channelFrom(r)
	rel, err := update.Latest(r.Context(), ch)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "upstream", Message: err.Error()})
		return
	}
	res, err := update.Apply(r.Context(), rel, version.Version, func(msg string, kv ...any) { s.log.Info("update: "+msg, kv...) })
	if err != nil {
		_ = s.store.Audit(r.Context(), u.Username, "update.failed", rel.Version, err.Error())
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "update_failed", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "update.applied", res.To, "from="+res.From)
	writeJSON(w, http.StatusOK, res)
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.log.Info("update installed, restarting", "to", res.To)
		if runtime.GOOS != "windows" {
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(syscall.SIGTERM)
				return
			}
		}
		os.Exit(0)
	}()
}
