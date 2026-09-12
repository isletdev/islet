package recipes

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/uptime"
)

const sample = `
name: Test recipe
slug: test
inputs:
  - { key: name, label: Name }
  - { key: repo, label: Repository, type: url }
  - { key: domain, label: Domain, optional: true }
steps:
  - { type: database, engine: postgres, as: db }
  - { type: app, as: app, repo: "{{repo}}", env: "DATABASE_URL={{db.url}}", domain: "{{domain}}" }
  - { type: deploy, app: app }
  - { type: uptime, app: app }
  - { type: backup, sources: ["database:{{db.instance}}", islet] }
done: "Open {{app.url}}"
`

func TestExpand(t *testing.T) {
	got := Expand("a={{x}} b={{ y.z }} c={{missing}}", map[string]string{"x": "1", "y.z": "2"})
	if got != "a=1 b=2 c=" {
		t.Fatal(got)
	}
}

func TestRunAndRollback(t *testing.T) {
	fsys := fstest.MapFS{"recipes/test.yaml": {Data: []byte(sample)}}
	var log []string
	deployFails := false
	h := Hooks{
		Database: func(ctx context.Context, actor, engine, inst, db string, out io.Writer) (string, bool, error) {
			log = append(log, "database "+inst+"/"+db)
			return "postgres://u:p@" + inst + "-db-1:5432/" + db, true, nil
		},
		RemoveStack: func(ctx context.Context, actor, name string) error { log = append(log, "undo stack "+name); return nil },
		SaveApp: func(ctx context.Context, a *deploy.App) (*deploy.App, error) {
			log = append(log, "app "+a.Name+" env="+a.Env+" domain="+a.Domain)
			a.ID, a.URL = "app1", "https://"+a.Domain
			return a, nil
		},
		DeleteApp: func(ctx context.Context, actor, id string) error { log = append(log, "undo app "+id); return nil },
		Deploy: func(ctx context.Context, actor, id string, out io.Writer) error {
			log = append(log, "deploy "+id)
			if deployFails {
				return errors.New("build failed")
			}
			return nil
		},
		SaveCheck: func(ctx context.Context, c *uptime.Check) (*uptime.Check, error) {
			log = append(log, "check "+c.Target)
			c.ID = "chk1"
			return c, nil
		},
		DeleteCheck: func(ctx context.Context, id string) error { log = append(log, "undo check "+id); return nil },
		Backup: func(ctx context.Context, actor, name string, sources []string, out io.Writer) (bool, error) {
			log = append(log, "backup "+strings.Join(sources, ","))
			return false, nil
		},
		PreviewHost: func(ctx context.Context, name string) string { return name + ".1-2-3-4.sslip.io" },
		SaveJob:     func(ctx context.Context, actor string, j *cron.Job) (*cron.Job, error) { return j, nil },
		DeleteJob:   func(ctx context.Context, id string) error { return nil },
		Install: func(ctx context.Context, actor string, req catalog.InstallRequest, out io.Writer) error {
			return nil
		},
	}
	e := New(fsys, h)
	var out strings.Builder
	if err := e.Run(context.Background(), "t", "test", map[string]string{"name": "Shop", "repo": "https://x/y.git"}, &out); err != nil {
		t.Fatal(err, out.String())
	}
	want := []string{"database shop-postgres/shop", "app shop env=DATABASE_URL=postgres://u:p@shop-postgres-db-1:5432/shop domain=shop.1-2-3-4.sslip.io", "deploy app1", "check https://shop.1-2-3-4.sslip.io", "backup database:shop-postgres,islet"}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(out.String(), "done. Open https://shop.1-2-3-4.sslip.io") {
		t.Fatalf("done line missing:\n%s", out.String())
	}

	// Failure in step 3 undoes the app and the database, in reverse order.
	log, deployFails = nil, true
	out.Reset()
	err := e.Run(context.Background(), "t", "test", map[string]string{"name": "shop", "repo": "https://x/y.git", "domain": "shop.example.com"}, &out)
	if err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatal("expected failure", err)
	}
	tail := strings.Join(log[len(log)-2:], "|")
	if tail != "undo app app1|undo stack shop-postgres" {
		t.Fatalf("rollback order: %s", tail)
	}
	if _, err := e.Get("nope"); err == nil {
		t.Fatal("unknown recipe should fail")
	}
	if err := e.Run(context.Background(), "t", "test", map[string]string{"name": "x"}, &out); err == nil || !strings.Contains(err.Error(), "Repository is required") {
		t.Fatal("missing input should fail", err)
	}
}
