// Package catalog loads app templates and installs them as managed Compose
// stacks with generated secrets and an optional domain.
package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	embedded "github.com/isletdev/islet/catalog"
	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/proxy"
)

// Field is one input on the install form.
type Field struct {
	Key   string `yaml:"key" json:"key"`
	Label string `yaml:"label" json:"label"`
	// Type is text, number, password, secret (generated) or select.
	Type    string `yaml:"type" json:"type"`
	Default string `yaml:"default" json:"default"`
	Hint    string `yaml:"hint" json:"hint,omitempty"`
	// Options are the choices for a select. A setting with three valid spellings
	// — Pocket ID's signup mode is disabled, withToken or open — is a list to
	// pick from, not a box to retype one of them into.
	Options []Option `yaml:"options" json:"options,omitempty"`
	// Required refuses an empty value at install time rather than letting the
	// container start without something it cannot run without.
	Required bool `yaml:"required" json:"required,omitempty"`
}

// Option is one choice on a select field.
type Option struct {
	Value string `yaml:"value" json:"value"`
	Label string `yaml:"label" json:"label"`
	Hint  string `yaml:"hint" json:"hint,omitempty"`
}

// App is one catalog entry.
type App struct {
	Name        string   `yaml:"name" json:"name"`
	Slug        string   `yaml:"slug" json:"slug"`
	Category    string   `yaml:"category" json:"category"`
	Description string   `yaml:"description" json:"description"`
	Website     string   `yaml:"website" json:"website"`
	Service     string   `yaml:"service" json:"service"` // service that receives the domain
	Port        int      `yaml:"port" json:"port"`
	Fields      []Field  `yaml:"fields" json:"fields"`
	Volumes     []string `yaml:"volumes" json:"volumes"`
	Notes       string   `yaml:"notes" json:"notes"`
	NeedsDomain bool     `yaml:"-" json:"needsDomain"`
	Compose     string   `yaml:"-" json:"compose,omitempty"`
}

// Service reads templates and installs them.
type Service struct {
	fsys   fs.FS
	docker *docker.Service
	proxy  *proxy.Manager
	stacks string
}

// New builds the service. stacksDir is where docker.Service keeps stacks.
func New(dk *docker.Service, px *proxy.Manager, stacksDir string) *Service {
	return &Service{fsys: &layered{under: embedded.FS}, docker: dk, proxy: px, stacks: stacksDir}
}

// List returns every app, sorted by category then name.
func (s *Service) List() ([]App, error) {
	entries, err := fs.ReadDir(s.fsys, "apps")
	if err != nil {
		return nil, err
	}
	out := []App{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		a, err := s.load(e.Name(), false)
		if err != nil {
			continue
		}
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Get returns one app with its compose file.
func (s *Service) Get(slug string) (*App, error) { return s.load(slug, true) }

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (s *Service) load(slug string, withCompose bool) (*App, error) {
	if !slugRe.MatchString(slug) {
		return nil, errors.New("bad slug")
	}
	b, err := fs.ReadFile(s.fsys, "apps/"+slug+"/islet.yaml")
	if err != nil {
		return nil, fmt.Errorf("no such app")
	}
	var a App
	if err := yaml.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	c, err := fs.ReadFile(s.fsys, "apps/"+slug+"/compose.yaml")
	if err != nil {
		return nil, err
	}
	a.NeedsDomain = strings.Contains(string(c), "${ISLET_DOMAIN}")
	if withCompose {
		a.Compose = string(c)
	}
	return &a, nil
}

// InstallRequest is what the UI sends.
type InstallRequest struct {
	Slug   string            `json:"slug"`
	Name   string            `json:"name"`   // stack name; defaults to slug
	Fields map[string]string `json:"fields"` // user-supplied values; secrets are generated when empty
	Domain string            `json:"domain"` // optional host to route
	TLS    string            `json:"tls"`    // letsencrypt | self | none
}

// Installed describes a stack created from the catalog.
type Installed struct {
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Domain      string            `json:"domain,omitempty"`
	InstalledAt string            `json:"installedAt"`
	Values      map[string]string `json:"values"` // generated secrets are kept so users can retrieve them
}

// Install writes the stack, starts it (streaming output) and routes the domain.
func (s *Service) Install(ctx context.Context, actor string, req InstallRequest) (io.ReadCloser, func() error, error) {
	app, err := s.load(req.Slug, true)
	if err != nil {
		return nil, nil, err
	}
	name := req.Name
	if name == "" {
		name = app.Slug
	}
	if !slugRe.MatchString(name) {
		return nil, nil, errors.New("name must be lowercase letters, digits or dashes")
	}
	if _, err := os.Stat(filepath.Join(s.stacks, name)); err == nil {
		return nil, nil, fmt.Errorf("a stack named %s already exists", name)
	}
	if app.NeedsDomain && req.Domain == "" {
		return nil, nil, errors.New("this app needs a domain")
	}
	values := map[string]string{}
	for _, f := range app.Fields {
		v := strings.TrimSpace(req.Fields[f.Key])
		switch {
		case v != "":
			values[f.Key] = v
		case f.Type == "secret":
			values[f.Key] = randomSecret(24)
		default:
			values[f.Key] = f.Default
		}
		// A select may only carry one of its own options: the values end up in
		// an environment variable the app parses, and a typo there is a
		// container that will not start with nothing on the page to say why.
		if f.Type == "select" && len(f.Options) > 0 {
			ok := false
			for _, o := range f.Options {
				if o.Value == values[f.Key] {
					ok = true
				}
			}
			if !ok {
				return nil, nil, fmt.Errorf("%s: %q is not one of the choices", f.Label, values[f.Key])
			}
		}
		if f.Required && values[f.Key] == "" {
			return nil, nil, errors.New(f.Label + " is needed")
		}
	}
	values["ISLET_DOMAIN"] = req.Domain
	var env strings.Builder
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&env, "%s=%s\n", k, values[k])
	}
	if err := s.docker.WriteStack(ctx, actor, name, app.Compose, env.String()); err != nil {
		return nil, nil, err
	}
	meta := Installed{Slug: app.Slug, Name: name, Domain: req.Domain, InstalledAt: time.Now().UTC().Format(time.RFC3339), Values: values}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(s.stacks, name, "islet-app.json"), mb, 0o600); err != nil {
		return nil, nil, err
	}
	rc, wait, err := s.docker.StackAction(ctx, actor, name, "up")
	if err != nil {
		return nil, nil, err
	}
	// Route the domain once the stack is up; the wait wrapper does it after compose finishes.
	wrapped := func() error {
		if err := wait(); err != nil {
			return err
		}
		if req.Domain != "" {
			tls := req.TLS
			if tls == "" {
				tls = "letsencrypt"
			}
			d := &proxy.Domain{Host: req.Domain, TargetType: "container", Target: name + "-" + app.Service + "-1", Port: app.Port, TLS: tls, Enabled: true}
			if _, err := s.proxy.Save(context.Background(), actor, d); err != nil {
				return fmt.Errorf("stack is up but the domain could not be routed: %w", err)
			}
		}
		return nil
	}
	return rc, wrapped, nil
}

// InstalledApps lists stacks that came from the catalog.
func (s *Service) InstalledApps() ([]Installed, error) {
	entries, err := os.ReadDir(s.stacks)
	if errors.Is(err, os.ErrNotExist) {
		return []Installed{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Installed{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(s.stacks, e.Name(), "islet-app.json"))
		if err != nil {
			continue
		}
		var it Installed
		if json.Unmarshal(b, &it) == nil {
			out = append(out, it)
		}
	}
	return out, nil
}

func randomSecret(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n*2]
}
