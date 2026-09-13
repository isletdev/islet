package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
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
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
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
	if !s.mcpEnabled(r.Context()) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "disabled", Message: "the MCP server is off; enable it in Settings"})
		return
	}
	tok := tokenFrom(r.Context())
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
	}
	return tools
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
}

func (b *bufferWriter) Header() http.Header {
	if b.header == nil {
		b.header = http.Header{}
	}
	return b.header
}
func (b *bufferWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufferWriter) WriteHeader(int)             {}

var _ = auth.ScopeAllows
