// Package api is the only package that knows about HTTP. It wires routes,
// middleware, and JSON encoding around the feature packages.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/backup"
	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/db"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/files"
	"github.com/isletdev/islet/internal/github"
	"github.com/isletdev/islet/internal/mcp"
	"github.com/isletdev/islet/internal/metrics"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/runner"
	"github.com/isletdev/islet/internal/security"
	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/internal/uptime"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

// Deps are the services the API exposes.
type Deps struct {
	Store    *store.Store
	Keys     *auth.Keys
	Auth     *auth.Service
	Metrics  *metrics.Collector
	Sampler  *metrics.Sampler
	Docker   *docker.Service
	Files    *files.Service
	Runner   *cmdrun.Runner
	Proxy    *proxy.Manager
	Catalog  *catalog.Service
	Notify   *notify.Bus
	Cron     *cron.Service
	DB       *db.Service
	Uptime   *uptime.Service
	Deploy   *deploy.Service
	Runners  *runner.Service
	Security *security.Service
	Backup   *backup.Service
	GitHub   *github.Client
	UI       http.Handler
	Log      *slog.Logger
}

// Server holds the dependencies handlers need.
type Server struct {
	store    *store.Store
	keys     *auth.Keys
	auth     *auth.Service
	metrics  *metrics.Collector
	sampler  *metrics.Sampler
	docker   *docker.Service
	files    *files.Service
	runner   *cmdrun.Runner
	proxy    *proxy.Manager
	catalog  *catalog.Service
	notify   *notify.Bus
	cron     *cron.Service
	db       *db.Service
	uptime   *uptime.Service
	deploy   *deploy.Service
	runners  *runner.Service
	security *security.Service
	backup   *backup.Service
	github   *github.Client
	mcp      *mcp.Server
	ui       http.Handler
	log      *slog.Logger
	started  time.Time
}

// New builds the HTTP handler for the daemon.
func New(d Deps) http.Handler {
	s := &Server{store: d.Store, keys: d.Keys, auth: d.Auth, metrics: d.Metrics, sampler: d.Sampler, docker: d.Docker, files: d.Files, runner: d.Runner, proxy: d.Proxy, catalog: d.Catalog, notify: d.Notify, cron: d.Cron, db: d.DB, uptime: d.Uptime, deploy: d.Deploy, runners: d.Runners, security: d.Security, backup: d.Backup, github: d.GitHub, ui: d.UI, log: d.Log, started: time.Now()}
	s.loadCookieDomain()
	s.StartWeeklyReport(context.Background())
	s.mcp = mcp.New(s.mcpTools(), auth.ScopeAllows)
	if s.deploy != nil {
		s.deploy.EnvGroup = s.EnvGroupLines
	}
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /_islet/maintenance", s.handleMaintenancePage)
	mux.HandleFunc("/_islet/auth", s.handleForwardAuth)
	mux.HandleFunc("GET /api/v1/auth/cookie-domain", s.requireAuth(s.handleCookieDomain))
	mux.HandleFunc("POST /api/v1/auth/cookie-domain", requireJSON(s.requireAuth(s.handleCookieDomain)))
	mux.HandleFunc("GET /api/v1/ping/{token}", s.handlePing)
	mux.HandleFunc("POST /api/v1/hooks/deploy/{id}", s.handleDeployHook)
	mux.HandleFunc("POST /api/v1/hooks/runner/{id}", s.handleRunnerHook)
	mux.HandleFunc("POST /api/v1/hooks/github", s.handleGitHubHook)
	mux.HandleFunc("POST /api/v1/ping/{token}", s.handlePing)
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", requireJSON(s.handleSetup))
	mux.HandleFunc("POST /api/v1/auth/login", requireJSON(s.handleLogin))
	mux.HandleFunc("POST /api/v1/auth/mfa", requireJSON(s.handleMFAVerify))
	mux.HandleFunc("POST /api/v1/auth/logout", requireJSON(s.handleLogout))

	// Signed in
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/v1/auth/password", requireJSON(s.requireAuth(s.handlePasswordChange)))
	mux.HandleFunc("POST /api/v1/auth/totp/setup", requireJSON(s.requireAuth(s.handleTOTPSetup)))
	mux.HandleFunc("POST /api/v1/auth/totp/enable", requireJSON(s.requireAuth(s.handleTOTPEnable)))
	mux.HandleFunc("POST /api/v1/auth/totp/disable", requireJSON(s.requireAuth(s.handleTOTPDisable)))
	mux.HandleFunc("GET /api/v1/auth/sessions", s.requireAuth(s.handleSessions))
	mux.HandleFunc("GET /api/v1/auth/tokens", s.requireAuth(s.handleTokens))
	mux.HandleFunc("GET /api/v1/users", s.requireAuth(s.handleUsers))
	mux.HandleFunc("POST /api/v1/users", requireJSON(s.requireAuth(s.handleUserCreate)))
	mux.HandleFunc("PUT /api/v1/users/{id}", requireJSON(s.requireAuth(s.handleUserUpdate)))
	mux.HandleFunc("DELETE /api/v1/users/{id}", requireJSON(s.requireAuth(s.handleUserDelete)))
	mux.HandleFunc("GET /api/v1/attention", s.requireAuth(s.handleAttention))
	mux.HandleFunc("GET /api/v1/github", s.requireAuth(s.handleGitHubConfig))
	mux.HandleFunc("POST /api/v1/github", requireJSON(s.requireAuth(s.handleGitHubSave)))
	mux.HandleFunc("GET /api/v1/github/repos", s.requireAuth(s.handleGitHubRepos))
	mux.HandleFunc("GET /api/v1/mcp", s.requireAuth(s.handleMCPSetting))
	mux.HandleFunc("GET /api/v1/report/weekly", s.requireAuth(s.handleReportSetting))
	mux.HandleFunc("POST /api/v1/report/weekly", requireJSON(s.requireAuth(s.handleReportSetting)))
	mux.HandleFunc("POST /api/v1/report/weekly/send", s.requireAuth(s.handleReportSend))
	mux.HandleFunc("POST /api/v1/mcp", requireJSON(s.requireAuth(s.handleMCPSetting)))
	mux.HandleFunc("/mcp", s.requireAuth(s.handleMCP))
	mux.HandleFunc("GET /api/v1/diagnostics", s.requireAuth(s.handleDiagnostics))
	mux.HandleFunc("POST /api/v1/auth/tokens", requireJSON(s.requireAuth(s.handleTokenCreate)))
	mux.HandleFunc("DELETE /api/v1/auth/tokens/{id}", requireJSON(s.requireAuth(s.handleTokenRevoke)))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{id}", requireJSON(s.requireAuth(s.handleSessionRevoke)))

	// System and metrics
	mux.HandleFunc("GET /api/v1/system", s.requireAuth(s.handleSystem))
	mux.HandleFunc("GET /api/v1/system/processes", s.requireAuth(s.handleProcesses))
	mux.HandleFunc("GET /api/v1/system/ports", s.requireAuth(s.handlePorts))
	mux.HandleFunc("GET /api/v1/metrics/latest", s.requireAuth(s.handleMetricsLatest))
	mux.HandleFunc("GET /api/v1/metrics/history", s.requireAuth(s.handleMetricsHistory))
	mux.HandleFunc("GET /api/v1/metrics/live", s.requireAuth(s.handleMetricsLive))
	mux.HandleFunc("GET /api/v1/terminal/ws", s.requireAuth(s.handleTerminal))
	mux.HandleFunc("GET /api/v1/audit", s.requireAuth(s.handleAudit))
	mux.HandleFunc("GET /api/v1/commands", s.requireAuth(s.handleCommands))

	// Docker
	// Files
	mux.HandleFunc("GET /api/v1/files", s.requireAuth(s.handleFilesList))
	mux.HandleFunc("GET /api/v1/files/read", s.requireAuth(s.handleFilesRead))
	mux.HandleFunc("GET /api/v1/files/tail", s.requireAuth(s.handleFilesTail))
	mux.HandleFunc("PUT /api/v1/files/write", requireJSON(s.requireAuth(s.handleFilesWrite)))
	mux.HandleFunc("POST /api/v1/files/op", requireJSON(s.requireAuth(s.handleFilesOp)))
	mux.HandleFunc("GET /api/v1/files/trash", s.requireAuth(s.handleTrashList))
	mux.HandleFunc("POST /api/v1/files/trash", requireJSON(s.requireAuth(s.handleTrashOp)))
	mux.HandleFunc("GET /api/v1/files/download", s.requireAuth(s.handleFilesDownload))
	mux.HandleFunc("POST /api/v1/files/upload", s.requireAuth(s.handleFilesUpload))
	mux.HandleFunc("GET /api/v1/files/search", s.requireAuth(s.handleFilesSearch))
	mux.HandleFunc("GET /api/v1/files/usage", s.requireAuth(s.handleFilesUsage))
	mux.HandleFunc("GET /api/v1/files/checksum", s.requireAuth(s.handleFilesChecksum))

	// Proxy and domains
	mux.HandleFunc("GET /api/v1/proxy", s.requireAuth(s.handleProxyStatus))
	mux.HandleFunc("POST /api/v1/proxy/install", requireJSON(s.requireAuth(s.handleProxyInstall)))
	mux.HandleFunc("POST /api/v1/proxy/remove", requireJSON(s.requireAuth(s.handleProxyRemove)))
	mux.HandleFunc("GET /api/v1/proxy/dns-providers", s.requireAuth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, proxy.DNSProviders) }))
	mux.HandleFunc("POST /api/v1/domains/import/nginx", requireJSON(s.requireAuth(s.handleNginxImport)))
	mux.HandleFunc("GET /api/v1/proxy/certs", s.requireAuth(s.handleProxyCerts))
	mux.HandleFunc("GET /api/v1/proxy/preview-host", s.requireAuth(s.handlePreviewHost))
	mux.HandleFunc("GET /api/v1/domains", s.requireAuth(s.handleDomains))
	mux.HandleFunc("POST /api/v1/domains", requireJSON(s.requireAuth(s.handleDomainSave)))
	mux.HandleFunc("PUT /api/v1/domains/{id}", requireJSON(s.requireAuth(s.handleDomainSave)))
	mux.HandleFunc("DELETE /api/v1/domains/{id}", requireJSON(s.requireAuth(s.handleDomainDelete)))
	mux.HandleFunc("GET /api/v1/domains/{id}/dns", s.requireAuth(s.handleDomainDNS))
	mux.HandleFunc("GET /api/v1/dns-check", s.requireAuth(s.handleDNSCheck))

	// Catalog
	mux.HandleFunc("GET /api/v1/catalog", s.requireAuth(s.handleCatalog))
	mux.HandleFunc("GET /api/v1/catalog/installed", s.requireAuth(s.handleInstalledApps))
	mux.HandleFunc("GET /api/v1/catalog/installed/updates", s.requireAuth(s.handleInstalledUpdates))
	mux.HandleFunc("POST /api/v1/catalog/installed/{name}/update", requireJSON(s.requireAuth(s.handleInstalledUpdate)))
	mux.HandleFunc("GET /api/v1/catalog/{slug}", s.requireAuth(s.handleCatalogApp))
	mux.HandleFunc("POST /api/v1/catalog/{slug}/install", requireJSON(s.requireAuth(s.handleCatalogInstall)))

	// Deploys
	mux.HandleFunc("GET /api/v1/apps", s.requireAuth(s.handleApps))
	mux.HandleFunc("POST /api/v1/apps", requireJSON(s.requireAuth(s.handleAppSave)))
	mux.HandleFunc("GET /api/v1/apps/env-groups", s.requireAuth(s.handleEnvGroups))
	mux.HandleFunc("POST /api/v1/apps/env-groups", requireJSON(s.requireAuth(s.handleEnvGroupSave)))
	mux.HandleFunc("DELETE /api/v1/apps/env-groups/{name}", requireJSON(s.requireAuth(s.handleEnvGroupDelete)))
	mux.HandleFunc("POST /api/v1/apps/inspect", requireJSON(s.requireAuth(s.handleAppInspect)))
	mux.HandleFunc("GET /api/v1/apps/{id}", s.requireAuth(s.handleApp))
	mux.HandleFunc("PUT /api/v1/apps/{id}", requireJSON(s.requireAuth(s.handleAppSave)))
	mux.HandleFunc("DELETE /api/v1/apps/{id}", requireJSON(s.requireAuth(s.handleAppDelete)))
	mux.HandleFunc("POST /api/v1/apps/{id}/deploy", requireJSON(s.requireAuth(s.handleAppDeploy)))
	mux.HandleFunc("GET /api/v1/apps/{id}/deploy/log", s.requireAuth(s.handleAppDeployLog))
	mux.HandleFunc("POST /api/v1/apps/{id}/services", requireJSON(s.requireAuth(s.handleAppAddService)))
	mux.HandleFunc("POST /api/v1/apps/{id}/cancel", requireJSON(s.requireAuth(s.handleAppCancel)))
	mux.HandleFunc("GET /api/v1/apps/{id}/releases", s.requireAuth(s.handleAppReleases))
	mux.HandleFunc("GET /api/v1/apps/{id}/releases/{release}", s.requireAuth(s.handleAppRelease))

	// Backups
	mux.HandleFunc("GET /api/v1/backups", s.requireAuth(s.handleBackupOverview))
	mux.HandleFunc("GET /api/v1/backups/kit", s.requireAuth(s.handleRecoveryKit))
	mux.HandleFunc("POST /api/v1/backups/destinations", requireJSON(s.requireAuth(s.handleDestinationSave)))
	mux.HandleFunc("PUT /api/v1/backups/destinations/{id}", requireJSON(s.requireAuth(s.handleDestinationSave)))
	mux.HandleFunc("DELETE /api/v1/backups/destinations/{id}", requireJSON(s.requireAuth(s.handleDestinationDelete)))
	mux.HandleFunc("POST /api/v1/backups/destinations/{id}/restore-test", requireJSON(s.requireAuth(s.handleDestinationRestoreTest)))
	mux.HandleFunc("POST /api/v1/backups/destinations/{id}/verify", requireJSON(s.requireAuth(s.handleDestinationVerify)))
	mux.HandleFunc("GET /api/v1/backups/destinations/{id}/snapshots", s.requireAuth(s.handleSnapshots))
	mux.HandleFunc("GET /api/v1/backups/destinations/{id}/snapshots/{snapshot}/ls", s.requireAuth(s.handleSnapshotLs))
	mux.HandleFunc("POST /api/v1/backups/destinations/{id}/restore", requireJSON(s.requireAuth(s.handleRestore)))
	mux.HandleFunc("POST /api/v1/backups/plans", requireJSON(s.requireAuth(s.handlePlanSave)))
	mux.HandleFunc("PUT /api/v1/backups/plans/{id}", requireJSON(s.requireAuth(s.handlePlanSave)))
	mux.HandleFunc("DELETE /api/v1/backups/plans/{id}", requireJSON(s.requireAuth(s.handlePlanDelete)))
	mux.HandleFunc("POST /api/v1/backups/plans/{id}/run", requireJSON(s.requireAuth(s.handlePlanRun)))
	mux.HandleFunc("POST /api/v1/backups/plans/{id}/cancel", requireJSON(s.requireAuth(s.handlePlanCancel)))
	mux.HandleFunc("GET /api/v1/backups/plans/{id}/runs", s.requireAuth(s.handlePlanRuns))
	mux.HandleFunc("GET /api/v1/backups/plans/{id}/runs/{run}", s.requireAuth(s.handlePlanRunDetail))

	// Security
	mux.HandleFunc("GET /api/v1/security", s.requireAuth(s.handleSecurityReport))
	mux.HandleFunc("POST /api/v1/security/fix-all", requireJSON(s.requireAuth(s.handleSecurityFixAll)))
	mux.HandleFunc("POST /api/v1/security/fix/{id}", requireJSON(s.requireAuth(s.handleSecurityFix)))
	mux.HandleFunc("POST /api/v1/security/firewall/rules", requireJSON(s.requireAuth(s.handleFirewallRule)))
	mux.HandleFunc("DELETE /api/v1/security/firewall/rules", requireJSON(s.requireAuth(s.handleFirewallRule)))
	mux.HandleFunc("POST /api/v1/security/unban", requireJSON(s.requireAuth(s.handleUnban)))
	mux.HandleFunc("POST /api/v1/security/ssh", requireJSON(s.requireAuth(s.handleSSHApply)))
	mux.HandleFunc("POST /api/v1/security/ssh/confirm", requireJSON(s.requireAuth(s.handleSSHConfirm)))
	mux.HandleFunc("POST /api/v1/security/scan", requireJSON(s.requireAuth(s.handleScanImage)))
	mux.HandleFunc("POST /api/v1/security/firewall/panel", requireJSON(s.requireAuth(s.handlePanelRestrict)))
	mux.HandleFunc("POST /api/v1/security/lynis", requireJSON(s.requireAuth(s.handleLynis)))
	mux.HandleFunc("POST /api/v1/security/panic", requireJSON(s.requireAuth(s.handlePanic)))

	// Runners
	mux.HandleFunc("GET /api/v1/runners", s.requireAuth(s.handleRunnerPools))
	mux.HandleFunc("POST /api/v1/runners", requireJSON(s.requireAuth(s.handleRunnerPoolSave)))
	mux.HandleFunc("PUT /api/v1/runners/{id}", requireJSON(s.requireAuth(s.handleRunnerPoolSave)))
	mux.HandleFunc("DELETE /api/v1/runners/{id}", requireJSON(s.requireAuth(s.handleRunnerPoolDelete)))
	mux.HandleFunc("GET /api/v1/runners/{id}/jobs", s.requireAuth(s.handleRunnerJobs))
	mux.HandleFunc("GET /api/v1/runners/{id}/workflow", s.requireAuth(s.handleRunnerWorkflow))

	// Uptime
	mux.HandleFunc("GET /api/v1/uptime/checks", s.requireAuth(s.handleChecks))
	mux.HandleFunc("POST /api/v1/uptime/checks", requireJSON(s.requireAuth(s.handleCheckSave)))
	mux.HandleFunc("PUT /api/v1/uptime/checks/{id}", requireJSON(s.requireAuth(s.handleCheckSave)))
	mux.HandleFunc("DELETE /api/v1/uptime/checks/{id}", requireJSON(s.requireAuth(s.handleCheckDelete)))
	mux.HandleFunc("GET /api/v1/uptime/checks/{id}/results", s.requireAuth(s.handleCheckResults))
	mux.HandleFunc("POST /api/v1/uptime/checks/{id}/probe", requireJSON(s.requireAuth(s.handleCheckProbe)))

	// Logs
	mux.HandleFunc("GET /api/v1/logs/sources", s.requireAuth(s.handleLogSources))
	mux.HandleFunc("GET /api/v1/logs/stream", s.requireAuth(s.handleLogStream))

	// Databases
	mux.HandleFunc("GET /api/v1/databases", s.requireAuth(s.handleDBList))
	mux.HandleFunc("GET /api/v1/databases/{name}", s.requireAuth(s.handleDBGet))
	mux.HandleFunc("POST /api/v1/databases/{name}/databases", requireJSON(s.requireAuth(s.handleDBCreate)))
	mux.HandleFunc("DELETE /api/v1/databases/{name}/databases/{db}", requireJSON(s.requireAuth(s.handleDBDrop)))
	mux.HandleFunc("GET /api/v1/databases/{name}/slow", s.requireAuth(s.handleDBSlow))
	mux.HandleFunc("POST /api/v1/databases/{name}/extensions", requireJSON(s.requireAuth(s.handleDBExtension)))
	mux.HandleFunc("POST /api/v1/databases/{name}/dumps", requireJSON(s.requireAuth(s.handleDBDump)))
	mux.HandleFunc("POST /api/v1/databases/{name}/restore", requireJSON(s.requireAuth(s.handleDBRestore)))
	mux.HandleFunc("GET /api/v1/databases/{name}/dumps/{file}", s.requireAuth(s.handleDBDumpDownload))
	mux.HandleFunc("DELETE /api/v1/databases/{name}/dumps/{file}", requireJSON(s.requireAuth(s.handleDBDumpDelete)))
	mux.HandleFunc("POST /api/v1/databases/{name}/schedule", requireJSON(s.requireAuth(s.handleDBSchedule)))
	mux.HandleFunc("POST /api/v1/databases/{name}/public", requireJSON(s.requireAuth(s.handleDBPublic)))

	// Cron
	mux.HandleFunc("GET /api/v1/cron/jobs", s.requireAuth(s.handleJobs))
	mux.HandleFunc("POST /api/v1/cron/jobs", requireJSON(s.requireAuth(s.handleJobSave)))
	mux.HandleFunc("GET /api/v1/cron/jobs/{id}", s.requireAuth(s.handleJob))
	mux.HandleFunc("PUT /api/v1/cron/jobs/{id}", requireJSON(s.requireAuth(s.handleJobSave)))
	mux.HandleFunc("DELETE /api/v1/cron/jobs/{id}", requireJSON(s.requireAuth(s.handleJobDelete)))
	mux.HandleFunc("POST /api/v1/cron/jobs/{id}/run", requireJSON(s.requireAuth(s.handleJobRun)))
	mux.HandleFunc("POST /api/v1/cron/jobs/{id}/kill", requireJSON(s.requireAuth(s.handleJobKill)))
	mux.HandleFunc("GET /api/v1/cron/jobs/{id}/runs", s.requireAuth(s.handleJobRuns))
	mux.HandleFunc("GET /api/v1/cron/jobs/{id}/runs/{run}", s.requireAuth(s.handleJobRunDetail))
	mux.HandleFunc("GET /api/v1/cron/jobs/{id}/versions", s.requireAuth(s.handleJobVersions))
	mux.HandleFunc("GET /api/v1/cron/jobs/{id}/versions/{version}", s.requireAuth(s.handleJobVersion))
	mux.HandleFunc("GET /api/v1/cron/jobs/{id}/export", s.requireAuth(s.handleJobExport))
	mux.HandleFunc("POST /api/v1/cron/preview", requireJSON(s.requireAuth(s.handleCronPreview)))
	mux.HandleFunc("GET /api/v1/cron/templates", s.requireAuth(s.handleCronTemplates))
	mux.HandleFunc("POST /api/v1/cron/lint", requireJSON(s.requireAuth(s.handleCronLint)))
	mux.HandleFunc("POST /api/v1/cron/import", requireJSON(s.requireAuth(s.handleCronImport)))

	// Notifications
	mux.HandleFunc("GET /api/v1/notify/channels", s.requireAuth(s.handleChannels))
	mux.HandleFunc("POST /api/v1/notify/channels", requireJSON(s.requireAuth(s.handleChannelSave)))
	mux.HandleFunc("PUT /api/v1/notify/channels/{id}", requireJSON(s.requireAuth(s.handleChannelSave)))
	mux.HandleFunc("DELETE /api/v1/notify/channels/{id}", requireJSON(s.requireAuth(s.handleChannelDelete)))
	mux.HandleFunc("POST /api/v1/notify/channels/{id}/test", requireJSON(s.requireAuth(s.handleChannelTest)))
	mux.HandleFunc("POST /api/v1/notify/telegram/detect", requireJSON(s.requireAuth(s.handleTelegramDetect)))
	mux.HandleFunc("GET /api/v1/notify/events", s.requireAuth(s.handleEvents))
	mux.HandleFunc("GET /api/v1/notify/events/{id}/deliveries", s.requireAuth(s.handleEventDeliveries))
	mux.HandleFunc("POST /api/v1/notify/emit", requireJSON(s.requireAuth(s.handleEmit)))

	mux.HandleFunc("GET /api/v1/docker/status", s.requireAuth(s.handleDockerStatus))
	mux.HandleFunc("GET /api/v1/docker/containers", s.requireAuth(s.handleContainers))
	mux.HandleFunc("GET /api/v1/docker/containers/{id}", s.requireAuth(s.handleContainer))
	mux.HandleFunc("GET /api/v1/docker/containers/{id}/logs", s.requireAuth(s.handleContainerLogs))
	mux.HandleFunc("GET /api/v1/docker/containers/{id}/exec", s.requireAuth(s.handleContainerExec))
	mux.HandleFunc("POST /api/v1/docker/containers/{id}/limits", requireJSON(s.requireAuth(s.handleContainerLimits)))
	mux.HandleFunc("POST /api/v1/docker/containers/{id}/{action}", requireJSON(s.requireAuth(s.handleContainerAction)))
	mux.HandleFunc("GET /api/v1/docker/images", s.requireAuth(s.handleImages))
	mux.HandleFunc("GET /api/v1/docker/registries", s.requireAuth(s.handleRegistries))
	mux.HandleFunc("POST /api/v1/docker/registries", requireJSON(s.requireAuth(s.handleRegistryLogin)))
	mux.HandleFunc("DELETE /api/v1/docker/registries/{host}", requireJSON(s.requireAuth(s.handleRegistryLogout)))
	mux.HandleFunc("POST /api/v1/docker/images/pull", requireJSON(s.requireAuth(s.handleImagePull)))
	mux.HandleFunc("DELETE /api/v1/docker/images/{id}", requireJSON(s.requireAuth(s.handleImageRemove)))
	mux.HandleFunc("GET /api/v1/docker/volumes", s.requireAuth(s.handleVolumes))
	mux.HandleFunc("DELETE /api/v1/docker/volumes/{name}", requireJSON(s.requireAuth(s.handleVolumeRemove)))
	mux.HandleFunc("GET /api/v1/docker/networks", s.requireAuth(s.handleNetworks))
	mux.HandleFunc("DELETE /api/v1/docker/networks/{name}", requireJSON(s.requireAuth(s.handleNetworkRemove)))
	mux.HandleFunc("GET /api/v1/docker/df", s.requireAuth(s.handleDockerDF))
	mux.HandleFunc("POST /api/v1/docker/prune", requireJSON(s.requireAuth(s.handleDockerPrune)))
	mux.HandleFunc("GET /api/v1/docker/stacks", s.requireAuth(s.handleStacks))
	mux.HandleFunc("POST /api/v1/docker/stacks/import", requireJSON(s.requireAuth(s.handleStackImport)))
	mux.HandleFunc("POST /api/v1/docker/stacks", requireJSON(s.requireAuth(s.handleStackWrite)))
	mux.HandleFunc("GET /api/v1/docker/stacks/{name}", s.requireAuth(s.handleStack))
	mux.HandleFunc("PUT /api/v1/docker/stacks/{name}", requireJSON(s.requireAuth(s.handleStackWrite)))
	mux.HandleFunc("DELETE /api/v1/docker/stacks/{name}", requireJSON(s.requireAuth(s.handleStackRemove)))
	mux.HandleFunc("POST /api/v1/docker/stacks/{name}/{action}", requireJSON(s.requireAuth(s.handleStackAction)))
	mux.HandleFunc("GET /api/v1/system/disk", s.requireAuth(s.handleDiskReport))
	mux.HandleFunc("POST /api/v1/system/disk/clean", requireJSON(s.requireAuth(s.handleDiskClean)))
	mux.HandleFunc("GET /api/v1/system/update", s.requireAuth(s.handleUpdateCheck))
	mux.HandleFunc("POST /api/v1/system/update", requireJSON(s.requireAuth(s.handleUpdateApply)))

	mux.HandleFunc("/api/", s.notFound)
	mux.Handle("/", s.ui)
	return s.recover(s.logRequests(s.securityHeaders(s.withSession(mux))))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Health{
		Status:        "ok",
		Version:       version.Version,
		Commit:        version.Commit,
		ServerID:      s.store.ServerID,
		Hostname:      s.store.Hostname,
		UptimeSeconds: int64(time.Since(s.started).Seconds()),
		Time:          time.Now().UTC(),
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such API route"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- middleware ----

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush lets streaming handlers (SSE, logs) push through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack lets WebSocket upgrades take over the connection.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.status = http.StatusSwitchingProtocols
		return h.Hijack()
	}
	return nil, nil, errors.New("response writer cannot be hijacked")
}

// Unwrap supports http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "path", r.URL.Path, "err", rec, "stack", string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
