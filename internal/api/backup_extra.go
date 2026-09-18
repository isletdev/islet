package api

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

// handleRestoreDatabase restores a dump from a snapshot into a brand-new
// database instance: restore the file, install the engine from the catalog,
// wait for it, load the dump. The live instance is never touched.
func (s *Server) handleRestoreDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct{ Snapshot, Path, Slug, NewInstance string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	u := userFrom(r.Context())
	parts := strings.Split(strings.TrimPrefix(req.Path, "/data/databases/"), "/")
	if !strings.HasPrefix(req.Path, "/data/databases/") || len(parts) != 2 || parts[1] == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "pick a dump file under databases/<instance>/ in the snapshot"})
		return
	}
	srcInst, file := parts[0], parts[1]
	slug := req.Slug
	if slug == "" {
		if inst, err := s.db.Get(r.Context(), u.Username, srcInst); err == nil {
			slug = inst.Slug
		}
	}
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "the original instance is gone; say which engine the dump is for (postgres, mysql, mariadb, mongodb)"})
		return
	}
	newName := strings.ToLower(strings.TrimSpace(req.NewInstance))
	if newName == "" {
		newName = srcInst + "-restored"
	}
	if _, err := s.db.Get(r.Context(), u.Username, newName); err == nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "an instance named " + newName + " already exists; pick a new name"})
		return
	}
	dir, err := s.backup.Restore(r.Context(), u.Username, r.PathValue("id"), req.Snapshot, req.Path, "", false)
	if err != nil {
		s.backupErr(w, err)
		return
	}
	restored := filepath.Join(dir, file)
	if _, err := os.Stat(restored); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "restore", Message: "the dump was not found after restore: " + restored})
		return
	}
	rc, wait, err := s.catalog.Install(r.Context(), u.Username, catalog.InstallRequest{Slug: slug, Name: newName})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: err.Error()})
		return
	}
	_, _ = io.Copy(io.Discard, rc)
	if err := wait(); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: "installing " + slug + " failed: " + err.Error()})
		return
	}
	// Wait until the engine answers.
	deadline := time.Now().Add(2 * time.Minute)
	var ready bool
	for time.Now().Before(deadline) {
		if inst, err := s.db.Get(r.Context(), u.Username, newName); err == nil && inst.State == "running" {
			if _, err := s.db.Databases(r.Context(), u.Username, inst); err == nil {
				ready = true
				break
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
	if !ready {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: newName + " did not become ready in two minutes; the dump is at " + restored})
		return
	}
	inst, _ := s.db.Get(r.Context(), u.Username, newName)
	dumpDir := filepath.Join(s.backup.DataDir(), "dumps", newName)
	_ = os.MkdirAll(dumpDir, 0o750)
	if err := copyFile(restored, filepath.Join(dumpDir, file)); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	database := strings.SplitN(file, "-", 2)[0]
	if err := s.db.Restore(r.Context(), u.Username, inst, file, database); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "restore", Message: "loading the dump failed: " + err.Error()})
		return
	}
	inst, _ = s.db.Get(r.Context(), u.Username, newName)
	_ = s.store.Audit(r.Context(), u.Username, "backup.restore-database", newName, req.Path)
	writeJSON(w, http.StatusOK, map[string]string{"instance": newName, "database": database, "internalUrl": inst.Internal})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// BackupHost is the rest-server this panel runs for other Islet servers.
type BackupHost struct {
	Domain   string `json:"domain"`
	TLS      string `json:"tls"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	URL      string `json:"url"`
}

const restServerStack = "islet-rest-server"

// handleBackupHost shows or sets up a restic rest-server on this panel so
// another Islet server can use it as an append-only destination.
func (s *Server) handleBackupHost(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	if r.Method == http.MethodGet {
		v, _, _ := s.store.Setting(r.Context(), "backup.host")
		var h BackupHost
		if v != "" {
			_ = json.Unmarshal([]byte(v), &h)
		}
		writeJSON(w, http.StatusOK, h)
		return
	}
	if r.Method == http.MethodDelete {
		_ = s.docker.RemoveStack(r.Context(), u.Username, restServerStack, false)
		v, _, _ := s.store.Setting(r.Context(), "backup.host")
		var h BackupHost
		if v != "" && json.Unmarshal([]byte(v), &h) == nil && h.Domain != "" {
			if doms, err := s.proxy.Domains(r.Context()); err == nil {
				for _, d := range doms {
					if d.Host == h.Domain && d.Target == restServerStack+"-rest-1" {
						_ = s.proxy.Delete(r.Context(), u.Username, d.ID)
					}
				}
			}
		}
		_ = s.store.SetSetting(r.Context(), "backup.host", "")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct{ Domain, TLS string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Domain == "" || strings.ContainsAny(req.Domain, " /:") {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "a host name for the backup endpoint is required"})
		return
	}
	if req.TLS == "" {
		req.TLS = "letsencrypt"
	}
	h := BackupHost{Domain: req.Domain, TLS: req.TLS, User: "islet", Password: randToken(16)}
	hash, err := bcrypt.GenerateFromPassword([]byte(h.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	compose := `services:
  rest:
    image: restic/rest-server:0.13.0
    restart: unless-stopped
    environment:
      OPTIONS: --append-only --private-repos --htpasswd-file /data/.htpasswd
    volumes:
      - data:/data
volumes:
  data:
`
	if err := s.docker.WriteStack(r.Context(), u.Username, restServerStack, compose, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	vol := restServerStack + "_data"
	_, _ = s.runner.Run(r.Context(), u.Username, "docker", "volume", "create", vol)
	line := h.User + ":" + string(hash)
	if _, err := s.runner.Run(r.Context(), u.Username, "docker", "run", "--rm", "-v", vol+":/data", "alpine:3", "sh", "-c", "printf '%s\\n' "+shellQuote(line)+" > /data/.htpasswd && mkdir -p /data/"+h.User+" && chown -R 1000:1000 /data"); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "docker", Message: "could not write credentials: " + err.Error()})
		return
	}
	rc, wait, err := s.docker.StackAction(r.Context(), u.Username, restServerStack, "up")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "docker", Message: err.Error()})
		return
	}
	_, _ = io.Copy(io.Discard, rc)
	if err := wait(); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "docker", Message: "rest-server did not start: " + err.Error()})
		return
	}
	container := restServerStack + "-rest-1"
	if err := s.proxy.Connect(r.Context(), u.Username, container); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "proxy", Message: err.Error()})
		return
	}
	var dom *proxy.Domain
	if doms, err := s.proxy.Domains(r.Context()); err == nil {
		for i := range doms {
			if doms[i].Host == req.Domain && doms[i].PathPrefix == "" {
				dom = &doms[i]
			}
		}
	}
	if dom == nil {
		dom = &proxy.Domain{Host: req.Domain, TLS: req.TLS, Enabled: true}
	}
	dom.TargetType, dom.Target, dom.Port, dom.TLS = "container", container, 8000, req.TLS
	if _, err := s.proxy.Save(r.Context(), u.Username, dom); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "proxy", Message: err.Error()})
		return
	}
	scheme := "https"
	if req.TLS == "none" {
		scheme = "http"
	}
	h.URL = scheme + "://" + req.Domain + "/" + h.User
	b, _ := json.Marshal(h)
	if err := s.store.SetSetting(r.Context(), "backup.host", string(b)); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "backup.host", req.Domain, "rest-server for other Islet servers")
	writeJSON(w, http.StatusOK, h)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func randToken(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(b)
}
