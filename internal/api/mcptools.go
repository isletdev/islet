package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/mcp"
)

// A curated tool is a description and a shape over one endpoint. It goes
// through the daemon's own router exactly as islet_request does, so there is
// one way into the API and one place where auth, the role check and the audit
// entry happen — the tools differ only in how well they describe themselves to
// the agent.
//
// Written as a table because forty handlers would be forty chances to get the
// dispatch subtly different, and because a tool that is three lines is a tool
// somebody will actually add when they add an endpoint.
type route struct {
	name, desc, scope, method, path string
	required                        []string
	props                           map[string]any
}

var placeholder = regexp.MustCompile(`\{([a-zA-Z]+)\}`)

// fillPath replaces {name} in a route template from the arguments. A missing
// one is an error rather than an empty segment: /api/v1/apps//deploy is a
// different route, and quietly calling it would be worse than refusing.
func fillPath(tmpl string, args map[string]any) (string, error) {
	var missing []string
	out := placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := m[1 : len(m)-1]
		v := strings.TrimSpace(mcp.Str(args, key))
		if v == "" {
			missing = append(missing, key)
			return m
		}
		return url.PathEscape(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s is required", strings.Join(missing, " and "))
	}
	return out, nil
}

// rest is everything not consumed by the path: the body of a write, the query
// of a read.
func rest(tmpl string, args map[string]any) map[string]any {
	used := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(tmpl, -1) {
		used[m[1]] = true
	}
	out := map[string]any{}
	for k, v := range args {
		if !used[k] && v != nil {
			out[k] = v
		}
	}
	return out
}

// viaRouter performs the call and records that an agent made it. Shared with
// islet_request so both leave the same trail.
func (s *Server) viaRouter(ctx context.Context, actor, method, path string, body map[string]any) (string, error) {
	var rdr io.Reader
	if len(body) > 0 {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, rdr)
	if err != nil {
		return "", err
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	} else {
		// NewRequest leaves Body nil for a request with no body, which is
		// right for a client and wrong here: net/http guarantees a server
		// handler a non-nil Body, and handlers rely on it. One that drains the
		// request before streaming panicked on the nil.
		req.Body = http.NoBody
	}
	rec := &bufferWriter{}
	s.routes.ServeHTTP(rec, req)
	out := strings.TrimSpace(rec.buf.String())
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		out = strings.TrimSpace(unwrapSSE(out))
	}
	_ = s.store.Audit(ctx, actor, "mcp.request", method+" "+path, strconv.Itoa(rec.code()))
	if rec.code() >= 400 {
		return "", fmt.Errorf("%s %s: %d %s", method, path, rec.code(), out)
	}
	if out == "" {
		return "done", nil
	}
	return out, nil
}

// unwrapSSE turns a recorded event stream back into the text it carried.
//
// Several endpoints answer in server-sent events because the panel follows
// them live. Through the recorder there is nothing live about it — the whole
// response is already there — and the framing is noise the agent would have to
// learn to read. Ping output should look like ping output.
func unwrapSSE(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "data: ")
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal([]byte(data), &s); err == nil {
			out = append(out, s)
			continue
		}
		out = append(out, data)
	}
	return strings.Join(out, "\n")
}

// curated turns a route into a tool. Resolve reports the real path rather than
// the template, so the scope and role gates are checked against the endpoint
// the call will actually reach.
func (s *Server) curated(r route) mcp.Tool {
	return mcp.Tool{
		Name: r.name, Description: r.desc, Scope: r.scope,
		Method: r.method, Path: r.path,
		InputSchema: mcp.Schema(r.required, r.props),
		Resolve: func(args map[string]any) (string, string, error) {
			p, err := fillPath(r.path, args)
			if err != nil {
				return "", "", err
			}
			return r.method, p, nil
		},
		Call: func(ctx context.Context, actor string, args map[string]any) (string, error) {
			p, err := fillPath(r.path, args)
			if err != nil {
				return "", err
			}
			extra := rest(r.path, args)
			if r.method == http.MethodGet || r.method == http.MethodDelete {
				if len(extra) > 0 {
					q := url.Values{}
					for k, v := range extra {
						q.Set(k, queryValue(v))
					}
					p += "?" + q.Encode()
				}
				return s.viaRouter(ctx, actor, r.method, p, nil)
			}
			return s.viaRouter(ctx, actor, r.method, p, extra)
		},
	}
}

// queryValue renders one argument as a query parameter.
//
// Two things that fmt.Sprint gets wrong here. Every handler in this package
// that reads a query boolean compares it against "1", and "true" is not that,
// so the flag would be dropped without a word. And a JSON number arrives as a
// float64, which prints in exponent form once it is large enough — a limit of
// ten million would have been sent as "1e+07".
func queryValue(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func str(desc string) map[string]any  { return mcp.P("string", desc) }
func num(desc string) map[string]any  { return mcp.P("number", desc) }
func flag(desc string) map[string]any { return mcp.P("boolean", desc) }

// curatedTools is the set worth describing properly: the flows an agent is
// actually asked for. Anything not here is still reachable with islet_request.
func (s *Server) curatedTools() []mcp.Tool {
	rs := []route{
		// ---- domains and the proxy -------------------------------------
		{"list_domains", "Every domain this server serves, with its target, TLS state and certificate.", "read", "GET", "/api/v1/domains", nil, map[string]any{}},
		{"create_domain", "Route a hostname to a container, an app, a port or the panel, and issue a certificate for it. This is how a site gets a name.", "domains", "POST", "/api/v1/domains", []string{"host", "targetType"},
			map[string]any{
				"host":         str("The hostname, for example shop.example.com"),
				"targetType":   str("container, url or panel. An app is routed by its container name; a local port is a url like http://127.0.0.1:8080"),
				"target":       str("Container name for container, an http(s) URL for url, nothing for panel"),
				"port":         num("Port inside the container, for targetType container"),
				"tls":          str("letsencrypt, letsencrypt-dns, self or none. Default letsencrypt"),
				"redirectWww":  flag("Also answer the www form and redirect it here"),
				"protect":      flag("Ask for an Islet login before the request reaches the app. The app keeps its own login; this keeps the outside world from seeing it"),
				"protectUsers": str("With protect, the usernames allowed through, comma separated. Empty means any signed-in Islet user"),
			}},
		{"delete_domain", "Stop serving a domain and remove its route.", "domains", "DELETE", "/api/v1/domains/{id}", []string{"id"}, map[string]any{"id": str("Domain id from list_domains")}},
		{"domain_dns", "What DNS record this domain needs and whether it resolves yet. Check this before expecting a certificate.", "read", "GET", "/api/v1/domains/{id}/dns", []string{"id"}, map[string]any{"id": str("Domain id")}},
		{"list_certificates", "Certificates the proxy holds, with expiry.", "read", "GET", "/api/v1/proxy/certs", nil, map[string]any{}},

		// ---- apps and deploys ------------------------------------------
		{"inspect_repo", "Look at a git repository and report the framework, build and start commands and port Islet would use. Run this before create_app to see what it will do.", "deploy", "POST", "/api/v1/apps/inspect", []string{"repoUrl"},
			map[string]any{"repoUrl": str("Git URL, for example https://github.com/owner/repo"), "branch": str("Branch, default the repository's own"), "rootDir": str("Subdirectory for a monorepo")}},
		{"create_app", "Create an app from a git repository, a Docker image or a local path and deploy it. Combine with create_domain to put it behind a hostname.", "deploy", "POST", "/api/v1/apps", []string{"name"},
			map[string]any{
				"name": str("App name, lowercase"), "source": str("git, image or upload"),
				"repoUrl": str("Git URL when source is git"), "branch": str("Branch to deploy"),
				"image": str("Docker image when source is image"), "rootDir": str("Subdirectory for a monorepo"),
				"env":  mcp.P("object", "Environment variables as name/value pairs"),
				"port": num("Port the app listens on"), "domain": str("Hostname to route to it"),
			}},
		{"get_app", "One app in full: source, status, environment, current release.", "read", "GET", "/api/v1/apps/{id}", []string{"id"}, map[string]any{"id": str("App name or id")}},
		{"delete_app", "Remove an app and its containers.", "deploy", "DELETE", "/api/v1/apps/{id}", []string{"id"}, map[string]any{"id": str("App name or id")}},
		{"app_deploy_log", "The live or last deploy log for an app. Read this when a deploy fails.", "read", "GET", "/api/v1/apps/{id}/deploy/log", []string{"id"}, map[string]any{"id": str("App name or id")}},
		{"cancel_deploy", "Stop a deploy that is running.", "deploy", "POST", "/api/v1/apps/{id}/cancel", []string{"id"}, map[string]any{"id": str("App name or id")}},
		{"add_app_service", "Attach a Postgres, MySQL or Redis instance to an app and inject its URL into the environment.", "deploy", "POST", "/api/v1/apps/{id}/services", []string{"id", "engine"},
			map[string]any{"id": str("App name or id"), "engine": str("postgres, mysql or redis")}},

		// ---- the catalog and recipes -----------------------------------
		{"list_catalog", "Apps that can be installed in one step, with what each needs.", "read", "GET", "/api/v1/catalog", nil, map[string]any{}},
		{"catalog_app", "One catalog entry: its form fields, ports, volumes and post-install notes.", "read", "GET", "/api/v1/catalog/{slug}", []string{"slug"}, map[string]any{"slug": str("Catalog slug, for example postgres")}},
		{"install_catalog_app", "Install a catalog app. Pass the entry's form fields as values.", "catalog", "POST", "/api/v1/catalog/{slug}/install", []string{"slug"},
			map[string]any{"slug": str("Catalog slug"), "name": str("Name for this installation"), "domain": str("Hostname to route to it"), "values": mcp.P("object", "Form fields from catalog_app")}},
		{"installed_catalog_apps", "Catalog apps installed here, and which have updates.", "read", "GET", "/api/v1/catalog/installed", nil, map[string]any{}},
		{"list_recipes", "Guided multi-step setups, each ending in something working.", "read", "GET", "/api/v1/recipes", nil, map[string]any{}},
		{"run_recipe", "Run a recipe end to end, with the inputs it asks for. Use list_recipes first to see what each one needs.", "catalog", "POST", "/api/v1/recipes/{slug}/run", []string{"slug"}, map[string]any{"slug": str("Recipe slug"), "inputs": mcp.P("object", "The recipe's inputs")}},

		// ---- databases --------------------------------------------------
		{"get_database", "One instance: engine, state, connection strings, databases and users.", "read", "GET", "/api/v1/databases/{name}", []string{"name"}, map[string]any{"name": str("Instance name")}},
		{"create_db_in_instance", "Create a database inside an instance.", "db", "POST", "/api/v1/databases/{name}/databases", []string{"name", "database"},
			map[string]any{"name": str("Instance name"), "database": str("Database to create"), "owner": str("Owner role, optional")}},
		{"dump_database", "Take a dump now and return where it was written.", "db", "POST", "/api/v1/databases/{name}/dumps", []string{"name"}, map[string]any{"name": str("Instance name"), "database": str("Which database, default all")}},
		{"database_slow_queries", "Slowest statements, from pg_stat_statements where available.", "read", "GET", "/api/v1/databases/{name}/slow", []string{"name"}, map[string]any{"name": str("Instance name")}},

		// ---- containers and stacks --------------------------------------
		{"get_container", "One container in full: image, state, ports, mounts, limits.", "read", "GET", "/api/v1/docker/containers/{id}", []string{"id"}, map[string]any{"id": str("Container name or id")}},
		{"list_stacks", "Compose stacks, managed and adoptable.", "read", "GET", "/api/v1/docker/stacks", nil, map[string]any{}},
		{"create_stack", "Create or replace a Compose stack from YAML.", "containers", "POST", "/api/v1/docker/stacks", []string{"name", "compose"},
			map[string]any{"name": str("Stack name"), "compose": str("The compose file, as YAML")}},
		{"stack_action", "up, down, pull or redeploy a stack.", "containers", "POST", "/api/v1/docker/stacks/{name}/{action}", []string{"name", "action"},
			map[string]any{"name": str("Stack name"), "action": str("up, down, pull or redeploy")}},
		{"disk_usage", "What Docker is using: images, containers, volumes, build cache.", "read", "GET", "/api/v1/docker/df", nil, map[string]any{}},

		// ---- files -------------------------------------------------------
		{"list_files", "List a directory on the server.", "files", "GET", "/api/v1/files", []string{"path"}, map[string]any{"path": str("Absolute path")}},
		{"read_file", "Read a text file from the server. Returns its contents, and says so when the file is binary or too large.", "files", "GET", "/api/v1/files/read", []string{"path"}, map[string]any{"path": str("Absolute path")}},
		{"search_files", "Search for files by name or content.", "files", "GET", "/api/v1/files/search", []string{"path", "query"},
			map[string]any{"path": str("Directory to search"), "query": str("What to look for"), "content": flag("Search inside files as well as names")}},
		{"file_operation", "Create, rename, move, copy, delete or change the mode of a path.", "files", "POST", "/api/v1/files/op", []string{"op"},
			map[string]any{"op": str("mkdir, rename, move, copy, delete, chmod or write"), "path": str("Target path"), "to": str("Destination for rename, move and copy"), "content": str("File contents for write"), "mode": str("Octal mode for chmod")}},

		// ---- cron ---------------------------------------------------------
		{"get_job", "One cron job in full: schedule, what it runs, where, and how its last runs went.", "read", "GET", "/api/v1/cron/jobs/{id}", []string{"id"}, map[string]any{"id": str("Job id or name")}},
		{"create_job", "Create a scheduled job: a command, a script, a container exec or an HTTP check.", "cron", "POST", "/api/v1/cron/jobs", []string{"name", "type", "schedule"},
			map[string]any{"name": str("Job name"), "type": str("command, script, container, http, chain or heartbeat"), "schedule": str("Cron expression, for example 0 3 * * *"), "command": str("What to run for a command job"), "script": str("Script body for a script job"), "container": str("Container for a container job"), "timezone": str("Timezone, default UTC")}},
		{"delete_job", "Remove a cron job and stop it running. Its history goes with it.", "cron", "DELETE", "/api/v1/cron/jobs/{id}", []string{"id"}, map[string]any{"id": str("Job id")}},
		{"job_runs", "Run history for a job, newest first.", "read", "GET", "/api/v1/cron/jobs/{id}/runs", []string{"id"}, map[string]any{"id": str("Job id")}},

		// ---- security -------------------------------------------------------
		{"apply_security_fix", "Apply one Security Score fix by id: firewall, fail2ban, auto-updates, swap, ntp, apt-upgrade, ssh-harden, firewall-routes.", "security", "POST", "/api/v1/security/fix/{id}", []string{"id"}, map[string]any{"id": str("Fix id from security_report")}},
		{"host_audit", "rkhunter, SUID and world-writable findings, and the /etc baseline diff.", "read", "GET", "/api/v1/security/host/audit", nil, map[string]any{}},
		{"add_firewall_rule", "Open or restrict a port in ufw.", "security", "POST", "/api/v1/security/firewall/rules", []string{"port"},
			map[string]any{"port": str("Port or port/proto, for example 5432/tcp"), "from": str("Source address or CIDR, default anywhere"), "comment": str("Why this rule exists")}},

		// ---- backups ---------------------------------------------------------
		{"run_backup", "Run a backup plan now rather than waiting for its schedule, and return the run it started.", "backups", "POST", "/api/v1/backups/plans/{id}/run", []string{"id"}, map[string]any{"id": str("Plan id")}},
		{"list_snapshots", "Snapshots at a destination.", "read", "GET", "/api/v1/backups/destinations/{id}/snapshots", []string{"id"}, map[string]any{"id": str("Destination id")}},
		{"verify_destination", "Check a repository's integrity and report the result.", "backups", "POST", "/api/v1/backups/destinations/{id}/verify", []string{"id"}, map[string]any{"id": str("Destination id")}},

		// ---- workspaces -------------------------------------------------------
		{"list_workspaces", "Workspaces and the agents in them.", "read", "GET", "/api/v1/workspaces", nil, map[string]any{}},
		{"create_workspace", "Create a workspace: a directory and a tmux session that outlives the tab.", "workspaces", "POST", "/api/v1/workspaces", []string{"name", "directory", "preset"},
			map[string]any{"name": str("Workspace name"), "directory": str("Working directory"), "preset": str("claude, shell or custom")}},
		{"delete_workspace", "Remove a workspace and end its session.", "workspaces", "DELETE", "/api/v1/workspaces/{id}", []string{"id"}, map[string]any{"id": str("Workspace id")}},

		// ---- uptime -----------------------------------------------------------
		{"create_uptime_check", "Watch a URL, a port or a keyword and alert when it goes down.", "uptime", "POST", "/api/v1/uptime/checks", []string{"name", "type", "target"},
			map[string]any{"name": str("Check name"), "type": str("http, tcp or keyword"), "target": str("URL or host:port"), "keyword": str("Text that must appear, for a keyword check")}},
		{"delete_uptime_check", "Delete an uptime check, so this server stops watching that target and alerting on it.", "uptime", "DELETE", "/api/v1/uptime/checks/{id}", []string{"id"}, map[string]any{"id": str("Check id")}},

		// ---- the vault ---------------------------------------------------
		// Listing and storing, and no reveal. An agent that could read every
		// secret on the server could copy the server; it can put one in and
		// refer to it by name, which is what it needs to set something up.
		{"list_secrets", "Names of the secrets stored in the vault, with what each is for and when it was last used. Values are never returned.", "read", "GET", "/api/v1/vault", nil, map[string]any{}},
		{"store_secret", "Put a value in the vault under a name, so it can be referred to as @vault:NAME in an app's environment or a cron command instead of being written out. Replaces the value if the name is already in use.", "vault", "POST", "/api/v1/vault", []string{"name", "value"},
			map[string]any{"name": str("UPPER_CASE_NAME, letters digits and underscores"), "value": str("The secret itself"), "description": str("What it is for")}},

		// ---- the server itself --------------------------------------------------
		{"metrics_history", "CPU, memory, disk and network over time.", "read", "GET", "/api/v1/metrics/history", nil, map[string]any{"range": str("How far back: 1h, 6h, 24h or 7d. Default 1h")}},
		{"diagnostics", "ping, traceroute, dig and a port check from this server.", "read", "GET", "/api/v1/diagnostics", []string{"host", "tool"}, map[string]any{"host": str("What to test"), "tool": str("ping, traceroute, dig or port")}},
		{"dns_check", "Does this hostname resolve here, and to what.", "read", "GET", "/api/v1/dns-check", []string{"host"}, map[string]any{"host": str("Hostname to resolve")}},
		{"audit_log", "What has been done on this server and by whom, newest first.", "read", "GET", "/api/v1/audit", nil, map[string]any{"limit": num("How many entries, default 100")}},
		{"commands_run", "Every command the daemon has run, with its output, secrets removed.", "read", "GET", "/api/v1/commands", nil, map[string]any{"limit": num("How many, default 100")}},
	}
	out := make([]mcp.Tool, 0, len(rs))
	for _, r := range rs {
		out = append(out, s.curated(r))
	}
	return out
}
