package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/mcp"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/pkg/api"
)

const mcpSetting = "mcp.enabled"

func (s *Server) mcpEnabled(ctx context.Context) bool {
	v, _, _ := s.store.Setting(ctx, mcpSetting)
	return v == "1"
}

// handleMCPSetting toggles the MCP endpoint (admins).
func (s *Server) handleMCPSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": s.mcpEnabled(r.Context()), "url": "/mcp"})
		return
	}
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	v := "0"
	if req.Enabled {
		v = "1"
	}
	if err := s.store.SetSetting(r.Context(), mcpSetting, v); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "mcp.toggle", "", v)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled, "url": "/mcp"})
}

// handleMCP is the Streamable HTTP endpoint. Tokens only; sessions are
// refused so a browser cannot be tricked into driving it.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	tok := tokenFrom(r.Context())
	// The switch in Settings decides whether *other people's* agents may reach
	// this daemon over the network. The assistant is this daemon calling itself
	// with a token it minted a second ago for one run, and making that wait on
	// a toggle somewhere else is how a server ends up with an assistant that
	// announces seventy tools and cannot use one of them. Nothing is opened by
	// this: a stranger still has no token, and these last minutes.
	if !s.mcpEnabled(r.Context()) && !s.isAssistantToken(tok) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "disabled", Message: "the MCP server is off; enable it in Settings"})
		return
	}
	u := userFrom(r.Context())
	if tok == nil || u == nil {
		writeJSON(w, http.StatusUnauthorized, api.Error{Error: "token_required", Message: "send an API token as Authorization: Bearer"})
		return
	}
	if r.Method == http.MethodGet {
		// No server-initiated stream; tell the client to use POST.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_body", Message: err.Error()})
		return
	}
	out := s.mcp.Handle(r.Context(), "mcp:"+u.Username, tok.Scopes, u.Role, body)
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// mcpTools builds the tool set over the daemon's services.
func (s *Server) mcpTools() []mcp.Tool {
	tools := []mcp.Tool{
		{Name: "server_status", Description: "Version, hostname, uptime and the latest CPU, memory and disk sample.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/system",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				info := map[string]any{"hostname": s.store.Hostname, "serverId": s.store.ServerID, "uptime": time.Since(s.started).Round(time.Second).String()}
				if s.sampler != nil {
					info["latest"] = s.sampler.Latest()
				}
				return mcp.JSON(info), nil
			}},
		{Name: "attention", Description: "What needs attention: security score, checks down, failed deploys and jobs, backup state, critical events.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/attention",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				rec := &bufferWriter{}
				req, _ := http.NewRequestWithContext(ctx, "GET", "/api/v1/attention", nil)
				s.handleAttention(rec, req)
				return rec.buf.String(), nil
			}},
		{Name: "list_containers", Description: "Containers with state, image and live CPU and memory.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/docker/containers",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				list, err := s.docker.Containers(ctx, actor)
				return mcp.JSON(list), err
			}},
		{Name: "container_logs", Description: "Last lines of a container's logs.", InputSchema: mcp.Schema([]string{"container"}, map[string]any{"container": mcp.P("string", "Container name or id"), "lines": mcp.P("integer", "How many lines, default 100")}), Scope: "logs", Method: "GET", Path: "/api/v1/docker/containers/x/logs",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				rc, wait, err := s.docker.Logs(ctx, actor, mcp.Str(args, "container"), mcp.Int(args, "lines", 100), false)
				if err != nil {
					return "", err
				}
				return mcp.Drain(rc, wait, 64<<10)
			}},
		{Name: "container_action", Description: "start, stop or restart a container.", InputSchema: mcp.Schema([]string{"container", "action"}, map[string]any{"container": mcp.P("string", "Container name"), "action": mcp.P("string", "start | stop | restart")}), Scope: "containers", Method: "POST", Path: "/api/v1/docker/containers/x/restart",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				act := mcp.Str(args, "action")
				if act != "start" && act != "stop" && act != "restart" {
					return "", errors.New("action must be start, stop or restart")
				}
				if err := s.docker.ContainerAction(ctx, actor, mcp.Str(args, "container"), act); err != nil {
					return "", err
				}
				return "ok", nil
			}},
		{Name: "list_apps", Description: "Deployed apps with status, URL and last release.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/apps",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				list, err := s.deploy.List(ctx)
				for i := range list {
					maskApp(&list[i], "viewer")
				}
				return mcp.JSON(list), err
			}},
		{Name: "deploy_app", Description: "Start a deploy of an app's branch head. Returns immediately; poll list_apps or app_releases for the result.", InputSchema: mcp.Schema([]string{"app"}, map[string]any{"app": mcp.P("string", "App name")}), Scope: "deploy", Method: "POST", Path: "/api/v1/apps/x/deploy",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				id, err := s.appIDByName(ctx, mcp.Str(args, "app"))
				if err != nil {
					return "", err
				}
				rel, err := s.deploy.Deploy(ctx, actor, id, "api", 0)
				if err != nil {
					return "", err
				}
				_ = s.store.Audit(ctx, actor, "app.deploy", id, "mcp")
				return "deploy started: release #" + itoa(rel.Number), nil
			}},
		{Name: "app_releases", Description: "Release history of an app, newest first, with status and errors.", InputSchema: mcp.Schema([]string{"app"}, map[string]any{"app": mcp.P("string", "App name")}), Scope: "read", Method: "GET", Path: "/api/v1/apps",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				id, err := s.appIDByName(ctx, mcp.Str(args, "app"))
				if err != nil {
					return "", err
				}
				list, err := s.deploy.Releases(ctx, id, 10)
				return mcp.JSON(list), err
			}},
		{Name: "list_jobs", Description: "Cron jobs with schedule, next run and last result.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/cron/jobs",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				list, err := s.cron.List(ctx)
				for i := range list {
					list[i].Script = ""
				}
				return mcp.JSON(list), err
			}},
		{Name: "run_job", Description: "Run a cron job now and return its output (waits up to five minutes).", InputSchema: mcp.Schema([]string{"job"}, map[string]any{"job": mcp.P("string", "Job name")}), Scope: "cron", Method: "POST", Path: "/api/v1/cron/jobs/x/run",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				jobs, err := s.cron.List(ctx)
				if err != nil {
					return "", err
				}
				for _, j := range jobs {
					if j.Name == mcp.Str(args, "job") || j.ID == mcp.Str(args, "job") {
						var buf bytes.Buffer
						err := s.cron.Run(ctx, j.ID, "api", &buf)
						out := buf.String()
						if len(out) > 64<<10 {
							out = out[len(out)-64<<10:]
						}
						if err != nil {
							return "", errors.New(err.Error() + "\n" + out)
						}
						return out, nil
					}
				}
				return "", errors.New("no such job")
			}},
		{Name: "notify", Description: "Send a notification through the configured channels.", InputSchema: mcp.Schema([]string{"title"}, map[string]any{"title": mcp.P("string", "Short title"), "message": mcp.P("string", "Body"), "severity": mcp.P("string", "info | warning | critical")}), Scope: "notify", Method: "POST", Path: "/api/v1/notify/emit",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				sev := mcp.Str(args, "severity")
				if sev == "" {
					sev = notify.Info
				}
				s.notify.Emit(ctx, notify.Event{Category: "custom", Severity: sev, Title: mcp.Str(args, "title"), Message: mcp.Str(args, "message"), Link: "/notifications"})
				return "sent", nil
			}},
		{Name: "recent_events", Description: "Recent events across the server (deploys, jobs, containers, security, backups).", InputSchema: mcp.Schema(nil, map[string]any{"limit": mcp.P("integer", "Default 30")}), Scope: "read", Method: "GET", Path: "/api/v1/notify/events",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				list, err := s.notify.Events(ctx, mcp.Int(args, "limit", 30), 0)
				return mcp.JSON(list), err
			}},
		{Name: "uptime_checks", Description: "Uptime checks with status and 24h/30d percentages.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/uptime/checks",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				list, err := s.uptime.List(ctx)
				return mcp.JSON(list), err
			}},
		{Name: "databases", Description: "Database instances with engine, state and container (no credentials).", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/databases",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				list, err := s.db.List(ctx, actor)
				for i := range list {
					list[i].Redact()
				}
				return mcp.JSON(list), err
			}},
		{Name: "backup_status", Description: "Backup plans, destinations, last success and staleness.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/backups",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				plans, _ := s.backup.Plans(ctx)
				return mcp.JSON(map[string]any{"health": s.backup.Health(ctx), "plans": plans}), nil
			}},
		{Name: "security_report", Description: "Security Score with every check and how to fix it.", InputSchema: mcp.Schema(nil, map[string]any{}), Scope: "read", Method: "GET", Path: "/api/v1/security",
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				return mcp.JSON(s.security.Report(ctx)), nil
			}},

		// The escape hatch.
		//
		// The named tools above cover the flows worth describing properly, and
		// there are 261 routes. Writing a tool for each would be thousands of
		// lines that drift from the API the moment anyone adds an endpoint, and
		// a tool list that long is worse for the agent reading it, not better.
		// This reaches the rest.
		//
		// It is not a way around anything. Resolve hands the method and path it
		// was asked for to the same two gates every other tool passes — the
		// token's scopes and the caller's role — and the call then goes through
		// the real router, so each handler's own auth, role check and audit
		// entry happen exactly as they do for a request off the network.
		{Name: "islet_request",
			Description: "Call any Islet REST endpoint that this token's scopes allow. Use the named tools first; this is for everything they do not cover. Paths look like /api/v1/domains. The OpenAPI description is in docs/openapi.yaml in the repository.",
			InputSchema: mcp.Schema([]string{"path"}, map[string]any{
				"path":   mcp.P("string", "Path beginning /api/v1/, including any query string"),
				"method": mcp.P("string", "GET, POST, PUT, PATCH or DELETE. Default GET"),
				"body":   mcp.P("object", "JSON body for POST, PUT and PATCH"),
			}),
			Scope: "whatever the route needs", Method: "GET", Path: "/api/v1/",
			Resolve: func(args map[string]any) (string, string, error) {
				method := strings.ToUpper(strings.TrimSpace(mcp.Str(args, "method")))
				if method == "" {
					method = http.MethodGet
				}
				switch method {
				case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				default:
					return "", "", fmt.Errorf("no target: %q is not a method this tool will send", method)
				}
				path := strings.TrimSpace(mcp.Str(args, "path"))
				if path == "" {
					return "", "", errors.New("no target: path is required, and looks like /api/v1/domains")
				}
				// Only this daemon's own API, and only by a path that cannot
				// climb out of it. The scope check that follows reads the path,
				// so anything that could make the checked path differ from the
				// called one has to be refused here.
				if !strings.HasPrefix(path, "/api/v1/") || strings.Contains(path, "..") {
					return "", "", fmt.Errorf("no target: %q is not an Islet API path", path)
				}
				// The gate matches on the path alone; the query goes to the
				// handler but must not be part of what is checked.
				if i := strings.IndexByte(path, '?'); i >= 0 {
					return method, path[:i], nil
				}
				return method, path, nil
			},
			Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
				method := strings.ToUpper(strings.TrimSpace(mcp.Str(args, "method")))
				if method == "" {
					method = http.MethodGet
				}
				path := strings.TrimSpace(mcp.Str(args, "path"))
				var body io.Reader
				if raw, ok := args["body"]; ok && raw != nil {
					b, err := json.Marshal(raw)
					if err != nil {
						return "", fmt.Errorf("body is not JSON: %w", err)
					}
					body = bytes.NewReader(b)
				}
				req, err := http.NewRequestWithContext(ctx, method, path, body)
				if err != nil {
					return "", err
				}
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
				}
				rec := &bufferWriter{}
				s.routes.ServeHTTP(rec, req)
				out := strings.TrimSpace(rec.buf.String())
				// The handler writes its own audit row under the account the
				// token belongs to, which is right — the agent acts as that
				// person. But then nothing says an agent did it. This row is
				// what separates "the admin added a cron job" from "something
				// the admin pointed at the server added a cron job".
				//
				// It covers whatever the route answered, a 4xx from the handler
				// included. A call the scope gate refused never reaches here, so
				// a token probing for what it cannot do leaves no trace —
				// auditing that needs a hook in internal/mcp, which has no
				// store, and is worth doing on its own.
				_ = s.store.Audit(ctx, actor, "mcp.request", method+" "+path, strconv.Itoa(rec.code()))
				if rec.code() >= 400 {
					// Returned as an error so the agent sees it failed rather
					// than reading a refusal as the answer.
					return "", fmt.Errorf("%s %s: %d %s", method, path, rec.code(), out)
				}
				return out, nil
			}},
	}
	// The curated set lives in mcptools.go as a table; these are the ones with
	// handlers of their own because they answer from the daemon's state rather
	// than from a route.
	return append(tools, s.curatedTools()...)
}

func (s *Server) appIDByName(ctx context.Context, name string) (string, error) {
	apps, err := s.deploy.List(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range apps {
		if a.Name == name || a.ID == name {
			return a.ID, nil
		}
	}
	return "", errors.New("no app named " + name)
}

func itoa(n int) string { return strconv.Itoa(n) }

// bufferWriter captures a handler's JSON output for reuse by tools.
type bufferWriter struct {
	buf    bytes.Buffer
	header http.Header
	status int
}

func (b *bufferWriter) Header() http.Header {
	if b.header == nil {
		b.header = http.Header{}
	}
	return b.header
}
func (b *bufferWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }

// Flush does nothing, and has to exist. A handler that streams checks for
// http.Flusher and answers "streaming unsupported" without it — which is what
// the diagnostics tool got every time it was called, a 500 that read like a
// broken server rather than a recorder that could not be flushed. Nothing is
// being flushed to: the buffer is the whole response, read once the handler
// returns.
func (b *bufferWriter) Flush() {}

// WriteHeader used to discard the status, which was harmless while every tool
// called one handler it already understood. A tool that can reach any route has
// to be able to tell a 200 from a 403, so the code is kept.
func (b *bufferWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferWriter) code() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}

var _ = auth.ScopeAllows
