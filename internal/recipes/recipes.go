// Package recipes runs multi-step setups (database + app + deploy + checks)
// from declarative YAML with progress output and rollback on failure.
package recipes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/uptime"
)

// Input is one question the wizard asks.
type Input struct {
	Key      string `yaml:"key" json:"key"`
	Label    string `yaml:"label" json:"label"`
	Type     string `yaml:"type" json:"type"` // text | url | domain | secret | select
	Default  string `yaml:"default" json:"default"`
	Hint     string `yaml:"hint" json:"hint,omitempty"`
	Optional bool   `yaml:"optional" json:"optional"`
	Options  string `yaml:"options" json:"options,omitempty"` // comma list for select
}

// Step is one action. Fields are templates: {{input}} and {{alias.output}}.
type Step struct {
	Type     string `yaml:"type" json:"type"` // database | app | deploy | install | uptime | cron | backup
	As       string `yaml:"as" json:"as,omitempty"`
	Label    string `yaml:"label" json:"label,omitempty"`
	Optional bool   `yaml:"optional" json:"optional,omitempty"`

	// database
	Engine   string `yaml:"engine" json:"engine,omitempty"`
	Name     string `yaml:"name" json:"name,omitempty"`
	Database string `yaml:"database" json:"database,omitempty"`

	// app
	Source     string `yaml:"source" json:"source,omitempty"`
	Repo       string `yaml:"repo" json:"repo,omitempty"`
	Branch     string `yaml:"branch" json:"branch,omitempty"`
	RootDir    string `yaml:"rootDir" json:"rootDir,omitempty"`
	Image      string `yaml:"image" json:"image,omitempty"`
	Domain     string `yaml:"domain" json:"domain,omitempty"`
	TLS        string `yaml:"tls" json:"tls,omitempty"`
	Env        string `yaml:"env" json:"env,omitempty"`
	Predeploy  string `yaml:"predeploy" json:"predeploy,omitempty"`
	Strategy   string `yaml:"strategy" json:"strategy,omitempty"`
	Volumes    string `yaml:"volumes" json:"volumes,omitempty"`
	Port       string `yaml:"port" json:"port,omitempty"`
	HealthPath string `yaml:"healthPath" json:"healthPath,omitempty"`
	Processes  string `yaml:"processes" json:"processes,omitempty"`
	StartCmd   string `yaml:"startCmd" json:"startCmd,omitempty"`

	// deploy / uptime
	App     string `yaml:"app" json:"app,omitempty"`
	Target  string `yaml:"target" json:"target,omitempty"`
	Keyword string `yaml:"keyword" json:"keyword,omitempty"`

	// install
	Slug   string            `yaml:"slug" json:"slug,omitempty"`
	Fields map[string]string `yaml:"fields" json:"fields,omitempty"`

	// cron
	Schedule  string `yaml:"schedule" json:"schedule,omitempty"`
	Command   string `yaml:"command" json:"command,omitempty"`
	Container string `yaml:"container" json:"container,omitempty"`

	// backup
	Sources []string `yaml:"sources" json:"sources,omitempty"`
}

// Recipe is a wizard definition.
type Recipe struct {
	Name        string  `yaml:"name" json:"name"`
	Slug        string  `yaml:"slug" json:"slug"`
	Category    string  `yaml:"category" json:"category"`
	Description string  `yaml:"description" json:"description"`
	Time        string  `yaml:"time" json:"time"`
	Inputs      []Input `yaml:"inputs" json:"inputs"`
	Steps       []Step  `yaml:"steps" json:"steps"`
	Done        string  `yaml:"done" json:"done"`
}

// Hooks are the panel services a run needs. Each returns what the undo
// step must know.
type Hooks struct {
	Install     func(ctx context.Context, actor string, req catalog.InstallRequest, out io.Writer) error
	RemoveStack func(ctx context.Context, actor, name string) error
	// Database installs the engine (if missing), waits for it and creates dbName. created says whether the instance is new.
	Database    func(ctx context.Context, actor, engine, instName, dbName string, out io.Writer) (url string, created bool, err error)
	SaveApp     func(ctx context.Context, a *deploy.App) (*deploy.App, error)
	DeleteApp   func(ctx context.Context, actor, id string) error
	Deploy      func(ctx context.Context, actor, id string, out io.Writer) error
	SaveCheck   func(ctx context.Context, c *uptime.Check) (*uptime.Check, error)
	DeleteCheck func(ctx context.Context, id string) error
	SaveJob     func(ctx context.Context, actor string, j *cron.Job) (*cron.Job, error)
	DeleteJob   func(ctx context.Context, id string) error
	// Backup creates a nightly plan when a destination exists; created=false means it was skipped.
	Backup      func(ctx context.Context, actor, name string, sources []string, out io.Writer) (created bool, err error)
	PreviewHost func(ctx context.Context, name string) string
}

// Engine loads recipes from the catalog filesystem and runs them.
type Engine struct {
	fsys fs.FS
	h    Hooks
}

func New(fsys fs.FS, h Hooks) *Engine { return &Engine{fsys: fsys, h: h} }

// List returns every recipe, sorted by name.
func (e *Engine) List() ([]Recipe, error) {
	entries, err := fs.ReadDir(e.fsys, "recipes")
	if err != nil {
		return []Recipe{}, nil
	}
	out := []Recipe{}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		r, err := e.load(strings.TrimSuffix(ent.Name(), ".yaml"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ent.Name(), err)
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (e *Engine) load(slug string) (*Recipe, error) {
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(slug) {
		return nil, errors.New("unknown recipe")
	}
	b, err := fs.ReadFile(e.fsys, "recipes/"+slug+".yaml")
	if err != nil {
		return nil, errors.New("unknown recipe")
	}
	var r Recipe
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Slug == "" {
		r.Slug = slug
	}
	return &r, nil
}

// Get returns one recipe.
func (e *Engine) Get(slug string) (*Recipe, error) { return e.load(slug) }

var tplRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// Expand substitutes {{key}} references from vars; unknown keys become "".
func Expand(s string, vars map[string]string) string {
	return tplRe.ReplaceAllStringFunc(s, func(m string) string {
		k := tplRe.FindStringSubmatch(m)[1]
		return vars[k]
	})
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

type undo struct {
	what string
	fn   func() error
}

// Run executes a recipe, streaming progress to w. On failure every change
// made so far is undone in reverse order.
func (e *Engine) Run(ctx context.Context, actor, slug string, inputs map[string]string, w io.Writer) (err error) {
	r, err := e.load(slug)
	if err != nil {
		return err
	}
	vars := map[string]string{}
	for _, in := range r.Inputs {
		v := strings.TrimSpace(inputs[in.Key])
		if v == "" {
			v = in.Default
		}
		if v == "" && !in.Optional {
			return fmt.Errorf("%s is required", in.Label)
		}
		if in.Key == "name" {
			v = strings.ToLower(v)
			if !nameRe.MatchString(v) {
				return errors.New("name must be 1-40 lowercase letters, digits or dashes")
			}
		}
		vars[in.Key] = v
	}
	if vars["domain"] == "" && e.h.PreviewHost != nil && vars["name"] != "" {
		vars["domain"] = e.h.PreviewHost(ctx, vars["name"])
		vars["tls"] = "letsencrypt"
	}
	if vars["tls"] == "" {
		vars["tls"] = "letsencrypt"
	}
	say := func(format string, a ...any) {
		if w != nil {
			fmt.Fprintf(w, format+"\n", a...)
		}
	}
	var undos []undo
	defer func() {
		if err == nil || len(undos) == 0 {
			return
		}
		say("[recipe] failed: %s", err)
		say("[recipe] undoing %d change(s)", len(undos))
		for i := len(undos) - 1; i >= 0; i-- {
			u := undos[i]
			if uerr := u.fn(); uerr != nil {
				say("[recipe]   could not undo %s: %s", u.what, uerr)
			} else {
				say("[recipe]   undid %s", u.what)
			}
		}
	}()
	say("[recipe] %s: %d steps", r.Name, len(r.Steps))
	x := func(s string) string { return Expand(s, vars) }
	for i, st := range r.Steps {
		label := st.Label
		if label == "" {
			label = st.Type
		}
		say("[recipe] step %d/%d: %s", i+1, len(r.Steps), x(label))
		as := st.As
		if as == "" {
			as = st.Type
		}
		var serr error
		switch st.Type {
		case "database":
			inst := x(st.Name)
			if inst == "" {
				inst = vars["name"] + "-" + st.Engine
			}
			dbName := x(st.Database)
			if dbName == "" {
				dbName = strings.ReplaceAll(vars["name"], "-", "_")
			}
			url, created, derr := e.h.Database(ctx, actor, st.Engine, inst, dbName, w)
			serr = derr
			if derr == nil {
				vars[as+".url"], vars[as+".instance"], vars[as+".database"] = url, inst, dbName
				if created {
					undos = append(undos, undo{"database instance " + inst, func() error { return e.h.RemoveStack(context.Background(), actor, inst) }})
				}
			}
		case "app":
			port, _ := strconv.Atoi(x(st.Port))
			src := x(st.Source)
			if src == "" {
				src = "git"
			}
			a := &deploy.App{Name: x(st.Name), Source: src, RepoURL: x(st.Repo), Branch: x(st.Branch), RootDir: x(st.RootDir), Image: x(st.Image), Domain: x(st.Domain), TLS: x(st.TLS), Env: strings.TrimSpace(x(st.Env)), PredeployCmd: x(st.Predeploy), Strategy: x(st.Strategy), Volumes: x(st.Volumes), Port: port, HealthPath: x(st.HealthPath), Processes: x(st.Processes), StartCmd: x(st.StartCmd), AutoDeploy: true}
			if a.Name == "" {
				a.Name = vars["name"]
			}
			if a.TLS == "" {
				a.TLS = vars["tls"]
			}
			saved, aerr := e.h.SaveApp(ctx, a)
			serr = aerr
			if aerr == nil {
				id := saved.ID
				vars[as+".id"], vars[as+".url"], vars[as+".name"], vars[as+".container"], vars[as+".domain"] = id, saved.URL, saved.Name, "islet-"+saved.Name, saved.Domain
				say("[recipe]   app %s created (%s)", saved.Name, saved.URL)
				undos = append(undos, undo{"app " + saved.Name, func() error { return e.h.DeleteApp(context.Background(), actor, id) }})
			}
		case "deploy":
			id := vars[firstNonEmpty(st.App, "app")+".id"]
			if id == "" {
				serr = errors.New("deploy step refers to an app that was not created")
			} else {
				serr = e.h.Deploy(ctx, actor, id, w)
			}
		case "install":
			req := catalog.InstallRequest{Slug: x(st.Slug), Name: x(st.Name), Domain: x(st.Domain), TLS: x(st.TLS), Fields: map[string]string{}}
			for k, v := range st.Fields {
				req.Fields[k] = x(v)
			}
			if req.Name == "" {
				req.Name = vars["name"]
			}
			if req.TLS == "" {
				req.TLS = vars["tls"]
			}
			serr = e.h.Install(ctx, actor, req, w)
			if serr == nil {
				name := req.Name
				scheme := "https"
				if req.TLS == "none" {
					scheme = "http"
				}
				vars[as+".name"], vars[as+".domain"], vars[as+".url"] = name, req.Domain, scheme+"://"+req.Domain
				undos = append(undos, undo{"stack " + name, func() error { return e.h.RemoveStack(context.Background(), actor, name) }})
			}
		case "uptime":
			c := &uptime.Check{Name: x(st.Name), Type: "http", Target: x(st.Target), Keyword: x(st.Keyword), IntervalSec: 60, TimeoutSec: 10, Enabled: true}
			if c.Name == "" {
				c.Name = vars["name"]
			}
			if c.Target == "" {
				c.Target = vars[firstNonEmpty(st.App, "app")+".url"]
			}
			if c.Target == "" {
				serr = errors.New("uptime step has no target")
				break
			}
			saved, cerr := e.h.SaveCheck(ctx, c)
			serr = cerr
			if cerr == nil {
				id := saved.ID
				undos = append(undos, undo{"uptime check " + saved.Name, func() error { return e.h.DeleteCheck(context.Background(), id) }})
			}
		case "cron":
			j := &cron.Job{Name: x(st.Name), Type: cron.TypeCommand, Schedule: x(st.Schedule), Command: x(st.Command), Container: x(st.Container), Enabled: true, TimeoutSec: 600}
			if j.Container != "" {
				j.Type = cron.TypeContainer
			}
			saved, jerr := e.h.SaveJob(ctx, actor, j)
			serr = jerr
			if jerr == nil {
				id := saved.ID
				undos = append(undos, undo{"cron job " + saved.Name, func() error { return e.h.DeleteJob(context.Background(), id) }})
			}
		case "backup":
			srcs := make([]string, 0, len(st.Sources))
			for _, s := range st.Sources {
				srcs = append(srcs, x(s))
			}
			created, berr := e.h.Backup(ctx, actor, vars["name"], srcs, w)
			serr = berr
			if berr == nil && !created {
				say("[recipe]   no backup destination yet; add one on the Backups page and the plan takes a minute")
			}
		default:
			serr = fmt.Errorf("unknown step type %q", st.Type)
		}
		if serr != nil {
			if st.Optional {
				say("[recipe]   skipped: %s", serr)
				continue
			}
			return fmt.Errorf("step %d (%s): %w", i+1, x(label), serr)
		}
	}
	say("[recipe] done. %s", strings.TrimSpace(x(r.Done)))
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
