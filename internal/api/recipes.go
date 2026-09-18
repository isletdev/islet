package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/backup"
	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/recipes"
	"github.com/isletdev/islet/internal/uptime"
	"github.com/isletdev/islet/pkg/api"
)

// recipeHooks wires the recipe engine to the panel's services.
func (s *Server) recipeHooks() recipes.Hooks {
	return recipes.Hooks{
		Install: func(ctx context.Context, actor string, req catalog.InstallRequest, out io.Writer) error {
			rc, wait, err := s.catalog.Install(ctx, actor, req)
			if err != nil {
				return err
			}
			sc := bufio.NewScanner(rc)
			for sc.Scan() {
				fmt.Fprintln(out, "  "+sc.Text())
			}
			return wait()
		},
		RemoveStack: func(ctx context.Context, actor, name string) error {
			return s.docker.RemoveStack(ctx, actor, name, true)
		},
		Database: s.provisionDatabase,
		SaveApp: func(ctx context.Context, a *deploy.App) (*deploy.App, error) {
			return s.deploy.Save(ctx, a)
		},
		DeleteApp: func(ctx context.Context, actor, id string) error { return s.deploy.Delete(ctx, actor, id) },
		Deploy: func(ctx context.Context, actor, id string, out io.Writer) error {
			rel, err := s.deploy.Deploy(ctx, actor, id, "recipe", 0)
			if err != nil {
				return err
			}
			past, ch, unsub := s.deploy.Subscribe(id)
			defer unsub()
			for _, l := range past {
				fmt.Fprintln(out, "  "+l)
			}
			if ch != nil {
				for l := range ch {
					fmt.Fprintln(out, "  "+l)
				}
			}
			r, err := s.deploy.Release(ctx, id, rel.ID)
			if err != nil {
				return err
			}
			if r.Status != "live" {
				msg := r.Error
				if msg == "" {
					msg = r.Status
				}
				return errors.New("deploy " + msg)
			}
			return nil
		},
		SaveCheck:   func(ctx context.Context, c *uptime.Check) (*uptime.Check, error) { return s.uptime.Save(ctx, c) },
		DeleteCheck: func(ctx context.Context, id string) error { return s.uptime.Delete(ctx, id) },
		SaveJob: func(ctx context.Context, actor string, j *cron.Job) (*cron.Job, error) {
			return s.cron.Save(ctx, actor, j)
		},
		DeleteJob: func(ctx context.Context, id string) error { return s.cron.Delete(ctx, id) },
		Backup: func(ctx context.Context, actor, name string, sources []string, out io.Writer) (bool, error) {
			if s.backup == nil {
				return false, nil
			}
			dests, err := s.backup.Destinations(ctx)
			if err != nil || len(dests) == 0 {
				return false, nil
			}
			p := &backup.Plan{Name: name + "-nightly", DestinationID: dests[0].ID, Schedule: "0 3 * * *", KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6, KeepYearly: 1, Enabled: true}
			for _, src := range sources {
				kind, val, _ := strings.Cut(src, ":")
				p.Sources = append(p.Sources, backup.Source{Type: kind, Value: val})
			}
			if _, err := s.backup.SavePlan(ctx, actor, p); err != nil {
				return false, err
			}
			fmt.Fprintf(out, "  plan %s created on %s\n", p.Name, dests[0].Name)
			return true, nil
		},
		PreviewHost: proxy.PreviewHost,
	}
}

// provisionDatabase installs an engine when the instance is missing, waits
// for it to accept connections and creates a database (SQL engines).
func (s *Server) provisionDatabase(ctx context.Context, actor, engine, instName, dbName string, out io.Writer) (string, bool, error) {
	slug := map[string]string{"postgres": "postgres", "mysql": "mysql", "mariadb": "mariadb", "redis": "redis", "mongo": "mongodb", "mongodb": "mongodb"}[engine]
	if slug == "" {
		return "", false, errors.New("unknown engine " + engine)
	}
	created := false
	if _, err := s.db.Get(ctx, actor, instName); err != nil {
		fmt.Fprintf(out, "  installing %s as %s\n", slug, instName)
		rc, wait, err := s.catalog.Install(ctx, actor, catalog.InstallRequest{Slug: slug, Name: instName, Fields: map[string]string{}})
		if err != nil {
			return "", false, err
		}
		sc := bufio.NewScanner(rc)
		for sc.Scan() {
			fmt.Fprintln(out, "  "+sc.Text())
		}
		if err := wait(); err != nil {
			return "", false, err
		}
		created = true
	} else {
		fmt.Fprintf(out, "  instance %s exists, reusing it\n", instName)
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		inst, err := s.db.Get(ctx, actor, instName)
		if err == nil && inst.State == "running" {
			if inst.Engine == "redis" {
				return inst.Internal, created, nil
			}
			if _, err := s.db.Databases(ctx, actor, inst); err == nil {
				url, err := s.db.CreateDatabase(ctx, actor, inst, dbName, dbName, "")
				if err != nil && strings.Contains(err.Error(), "already exists") {
					fmt.Fprintln(out, "  database exists; using the instance's primary URL")
					return inst.Internal, created, nil
				}
				if err != nil {
					return "", created, err
				}
				fmt.Fprintf(out, "  database %s created\n", dbName)
				return url, created, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", created, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return "", created, errors.New("the database did not become ready in two minutes")
}

func (s *Server) handleRecipes(w http.ResponseWriter, r *http.Request) {
	list, err := s.recipes.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRecipeRun runs a wizard and streams its progress.
func (s *Server) handleRecipeRun(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins run recipes"})
		return
	}
	var inputs map[string]string
	if err := decode(r, &inputs); err != nil {
		s.badJSON(w, err)
		return
	}
	slug := r.PathValue("slug")
	if _, err := s.recipes.Get(slug); err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "recipe.run", slug, "")
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if err := s.recipes.Run(r.Context(), u.Username, slug, inputs, pw); err != nil {
			fmt.Fprintln(pw, "error: "+err.Error())
		}
	}()
	streamLines(w, r, pr, func() error {
		pr.Close()
		return nil
	})
}
