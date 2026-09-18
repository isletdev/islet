package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/pkg/api"
)

// Disk Doctor: what is eating disk, and safe one-click cleanup.

type diskItem struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Bytes       int64  `json:"bytes"`
	Reclaimable int64  `json:"reclaimable"`
	Hint        string `json:"hint"`
	Cleanable   bool   `json:"cleanable"`
}

func (s *Server) handleDiskReport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	actor := userFrom(r.Context()).Username
	items := []diskItem{}

	// Docker
	if df, err := s.docker.SystemDF(ctx, actor); err == nil {
		for _, d := range df {
			key := strings.ToLower(strings.Fields(d.Type)[0])
			items = append(items, diskItem{Key: "docker-" + key, Label: "Docker " + strings.ToLower(d.Type), Bytes: parseSize(d.Size), Reclaimable: parseSize(d.Reclaimable),
				Hint: "Unused " + strings.ToLower(d.Type) + " can be removed with docker prune.", Cleanable: key != "local" || d.Reclaimable != "0B"})
		}
	}
	if runtime.GOOS == "linux" {
		items = append(items, s.dirItem(ctx, "journal", "systemd journal", "/var/log/journal", "Vacuum keeps the last 7 days.", true))
		items = append(items, s.dirItem(ctx, "apt", "apt package cache", "/var/cache/apt", "apt-get clean removes downloaded .deb files.", true))
		items = append(items, s.dirItem(ctx, "logs", "/var/log (other)", "/var/log", "Rotated logs older than 30 days can be removed.", false))
		items = append(items, s.dirItem(ctx, "tmp", "/tmp", "/tmp", "Files not touched in 7 days are safe to remove.", true))
	}
	items = append(items, s.dirItem(ctx, "trash", "Islet trash", s.files.TrashDir(), "Emptying the trash is permanent.", true))
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) dirItem(ctx context.Context, key, label, path, hint string, cleanable bool) diskItem {
	it := diskItem{Key: key, Label: label, Hint: hint, Cleanable: cleanable}
	if _, err := os.Stat(path); err != nil {
		return it
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if u, err := s.files.DiskUsage(c, path); err == nil {
		it.Bytes = u.Total
		if cleanable {
			it.Reclaimable = u.Total
		}
	}
	return it
}

func (s *Server) handleDiskClean(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can clean up"})
		return
	}
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	results := map[string]string{}
	for _, k := range req.Keys {
		var err error
		switch k {
		case "docker-images":
			res, e := s.docker.Prune(ctx, u.Username, docker.PruneOptions{Images: true, AllImages: true})
			err, results[k] = e, res["images"]
		case "docker-containers":
			res, e := s.docker.Prune(ctx, u.Username, docker.PruneOptions{Containers: true})
			err, results[k] = e, res["containers"]
		case "docker-build":
			res, e := s.docker.Prune(ctx, u.Username, docker.PruneOptions{Builder: true})
			err, results[k] = e, res["builder"]
		case "docker-local":
			// Volumes are never pruned from Disk Doctor; data loss must be an explicit choice on the Volumes tab.
			results[k] = "skipped: remove unused volumes from the Volumes tab"
		case "journal":
			if runtime.GOOS != "linux" {
				err = errUnsupported
				break
			}
			_, err = s.runner.Run(ctx, u.Username, "journalctl", "--vacuum-time=7d")
			results[k] = "kept last 7 days"
		case "apt":
			if runtime.GOOS != "linux" {
				err = errUnsupported
				break
			}
			_, err = s.runner.Run(ctx, u.Username, "apt-get", "clean")
			results[k] = "cache cleared"
		case "tmp":
			if runtime.GOOS != "linux" {
				err = errUnsupported
				break
			}
			_, err = s.runner.Run(ctx, u.Username, "find", "/tmp", "-mindepth", "1", "-atime", "+7", "-delete")
			results[k] = "old files removed"
		case "trash":
			err = s.files.PurgeTrash("")
			results[k] = "emptied"
		default:
			results[k] = "unknown"
		}
		if err != nil {
			results[k] = "error: " + err.Error()
		}
	}
	_ = s.store.Audit(r.Context(), u.Username, "disk.clean", strings.Join(req.Keys, ","), fmt.Sprint(results))
	writeJSON(w, http.StatusOK, results)
}

var errUnsupported = fmt.Errorf("not available on %s", runtime.GOOS)

// parseSize turns "12.3GB" / "456MB" / "0B" into bytes.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " ("); i > 0 {
		s = s[:i]
	}
	var v float64
	var unit string
	if _, err := fmt.Sscanf(s, "%f%s", &v, &unit); err != nil {
		return 0
	}
	mult := map[string]float64{"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "KiB": 1024, "MiB": 1 << 20, "GiB": 1 << 30}
	return int64(v * mult[unit])
}
