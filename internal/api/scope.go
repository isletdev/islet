package api

import (
	"context"
	"net/http"
	"path"
	"strings"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/pkg/api"
)

// Project scopes narrow a deployer or viewer to a set of apps (name globs)
// and everything that hangs off them: their containers, the databases
// created next to them and the domains routed to them. Admins are never
// scoped. An empty list means "every app the role allows", as before.

func scoped(u *auth.User) bool {
	return u != nil && u.Role != "admin" && strings.TrimSpace(u.Projects) != ""
}

func projectGlobs(u *auth.User) []string {
	var out []string
	for _, p := range strings.Split(u.Projects, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// allowsApp reports whether the user may see the app with that name.
func allowsApp(u *auth.User, appName string) bool {
	if !scoped(u) {
		return true
	}
	for _, g := range projectGlobs(u) {
		if ok, _ := path.Match(g, appName); ok || g == appName {
			return true
		}
	}
	return false
}

// allowedApps returns the names of the apps the user may see, or nil for
// an unscoped user (meaning all).
func (s *Server) allowedApps(ctx context.Context, u *auth.User) []string {
	if !scoped(u) {
		return nil
	}
	apps, err := s.deploy.List(ctx)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, a := range apps {
		if allowsApp(u, a.Name) {
			out = append(out, a.Name)
		}
	}
	return out
}

// allowsContainer matches islet-<app>-… containers and <app>-<service>-1
// stack containers (databases added next to an app) to the user's apps.
func (s *Server) allowsContainer(ctx context.Context, u *auth.User, container string) bool {
	if !scoped(u) {
		return true
	}
	for _, app := range s.allowedApps(ctx, u) {
		if strings.HasPrefix(container, "islet-"+app+"-") || strings.HasPrefix(container, app+"-") {
			return true
		}
	}
	return false
}

// allowsInstance matches database instances named <app>-<engine>.
func (s *Server) allowsInstance(ctx context.Context, u *auth.User, inst string) bool {
	if !scoped(u) {
		return true
	}
	for _, app := range s.allowedApps(ctx, u) {
		if strings.HasPrefix(inst, app+"-") || inst == app {
			return true
		}
	}
	return false
}

func forbiddenScope(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "this is outside your projects"})
}

// scopeApp guards /apps/{id} routes.
func (s *Server) scopeApp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if scoped(u) {
			a, err := s.deploy.Get(r.Context(), r.PathValue("id"))
			if err != nil || !allowsApp(u, a.Name) {
				forbiddenScope(w)
				return
			}
		}
		next(w, r)
	}
}

// scopeContainer guards /docker/containers/{id} routes; id may be a name or an id.
func (s *Server) scopeContainer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if scoped(u) {
			info, err := s.docker.Inspect(r.Context(), u.Username, r.PathValue("id"))
			if err != nil || !s.allowsContainer(r.Context(), u, info.Name) {
				forbiddenScope(w)
				return
			}
		}
		next(w, r)
	}
}

// scopeAdminOnly refuses scoped users outright (files, terminal): a project
// scope must not leak the rest of the host.
func scopeAdminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if scoped(userFrom(r.Context())) {
			forbiddenScope(w)
			return
		}
		next(w, r)
	}
}

// filterApps keeps the apps a scoped user may see.
func filterApps(u *auth.User, list []deploy.App) []deploy.App {
	if !scoped(u) {
		return list
	}
	out := make([]deploy.App, 0, len(list))
	for _, a := range list {
		if allowsApp(u, a.Name) {
			out = append(out, a)
		}
	}
	return out
}
