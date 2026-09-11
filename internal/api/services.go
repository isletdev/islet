package api

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/pkg/api"
)

// handleAppAddService installs a database next to an app, creates a
// database for it and injects the connection URL into the app's
// environment. Output streams like an install.
func (s *Server) handleAppAddService(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins add services"})
		return
	}
	a, err := s.deploy.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.deployErr(w, err)
		return
	}
	var req struct{ Engine string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	var slug, envKey string
	switch req.Engine {
	case "postgres":
		slug, envKey = "postgres", "DATABASE_URL"
	case "mysql":
		slug, envKey = "mysql", "DATABASE_URL"
	case "redis":
		slug, envKey = "redis", "REDIS_URL"
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "engine must be postgres, mysql or redis"})
		return
	}
	name := a.Name + "-" + req.Engine
	if len(name) > 40 {
		name = name[:40]
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		fmt.Fprintf(pw, "[islet] installing %s as %s\n", slug, name)
		if _, err := s.db.Get(r.Context(), u.Username, name); err != nil {
			rc, wait, err := s.catalog.Install(r.Context(), u.Username, catalog.InstallRequest{Slug: slug, Name: name, Fields: map[string]string{}})
			if err != nil {
				fmt.Fprintln(pw, "error: "+err.Error())
				return
			}
			sc := bufio.NewScanner(rc)
			for sc.Scan() {
				fmt.Fprintln(pw, sc.Text())
			}
			if err := wait(); err != nil {
				fmt.Fprintln(pw, "error: install failed: "+err.Error())
				return
			}
		} else {
			fmt.Fprintln(pw, "[islet] instance already exists, reusing it")
		}
		// Wait for the engine to accept connections.
		var url string
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			inst, err := s.db.Get(r.Context(), u.Username, name)
			if err == nil && inst.State == "running" {
				if req.Engine == "redis" {
					url = inst.Internal
					break
				}
				if _, err := s.db.Databases(r.Context(), u.Username, inst); err == nil {
					dbName := strings.ReplaceAll(a.Name, "-", "_")
					url, err = s.db.CreateDatabase(r.Context(), u.Username, inst, dbName, dbName, "")
					if err != nil && strings.Contains(err.Error(), "already exists") {
						url = inst.Internal
						fmt.Fprintln(pw, "[islet] database already exists; using the instance's primary URL")
						err = nil
					}
					if err != nil {
						fmt.Fprintln(pw, "error: "+err.Error())
						return
					}
					break
				}
			}
			time.Sleep(3 * time.Second)
		}
		if url == "" {
			fmt.Fprintln(pw, "error: the database did not become ready in 90 seconds")
			return
		}
		env := strings.TrimRight(a.Env, "\n")
		var lines []string
		for _, l := range strings.Split(env, "\n") {
			if !strings.HasPrefix(l, envKey+"=") && strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		lines = append(lines, envKey+"="+url)
		a.Env = strings.Join(lines, "\n")
		if _, err := s.deploy.Save(r.Context(), a); err != nil {
			fmt.Fprintln(pw, "error: could not save the app environment: "+err.Error())
			return
		}
		_ = s.store.Audit(r.Context(), u.Username, "app.service", a.ID, req.Engine+" -> "+envKey)
		fmt.Fprintf(pw, "[islet] %s set on %s. Redeploy to apply.\n", envKey, a.Name)
	}()
	streamLines(w, r, pr, func() error {
		pr.Close()
		return nil
	})
}
