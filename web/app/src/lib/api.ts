// Thin client for the daemon API. Types mirror pkg/api in Go; codegen replaces
// the hand-written ones once the OpenAPI spec exists.

export interface Health {
  status: string;
  version: string;
  commit: string;
  serverId: string;
  hostname: string;
  uptimeSeconds: number;
  time: string;
}

export interface ApiError {
  error: string;
  message: string;
}

export interface User {
  id: string;
  username: string;
  role: "admin" | "deployer" | "viewer";
  projects?: string;
  totpEnabled: boolean;
  createdAt: string;
  lastLoginAt?: string;
}

export interface Me {
  user: User;
  sessionId: string;
  recoveryCodesLeft: number;
}

export interface LoginResponse {
  mfaRequired: boolean;
  user?: User;
}

export interface Session {
  id: string;
  current: boolean;
  ip: string;
  userAgent: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
}

export interface Sample {
  ts: string; cpuPct: number; load1: number; load5: number; load15: number;
  memUsed: number; memTotal: number; swapUsed: number; swapTotal: number;
  diskUsed: number; diskTotal: number; netRx: number; netTx: number; ifaces?: { name: string; rx: number; tx: number }[];
}

export interface Point {
  ts: number; cpuPct: number; load1: number; memUsed: number; memTotal: number;
  diskUsed: number; diskTotal: number; netRx: number; netTx: number;
}

export interface HostInfo {
  hostname: string; os: string; platform: string; platformVersion: string; kernel: string; arch: string;
  cpuCount: number; cpuModel: string; memTotal: number; diskTotal: number; bootTime: number; uptimeSeconds: number; timezone: string;
}

export interface Process { pid: number; name: string; user: string; cpuPct: number; memRss: number; started: number }
export interface Port {
  container?: string; proto: string; address: string; port: number; pid: number; process: string }

export interface AuditEntry { id: number; actor: string; action: string; target: string; detail: string; createdAt: string }
export interface UpdateStatus { current: string; channel: string; latest: string; prerelease: boolean; publishedAt: string; updateAvailable: boolean; notes?: string }

export interface FleetServer {
  id: string; name: string; host: string; sshPort: number; sshUser: string; panelPort: number;
  status: "pending" | "joining" | "ready" | "unreachable" | "failed";
  statusNote?: string; version?: string; hostname?: string; lastSeen?: string; createdAt: string;
}
export interface FleetLocal { id: string; name: string; hostname: string; status: string; version: string }

export interface DockerStatus { available: boolean; version: string; composeVersion: string; error?: string }
export interface Container { id: string; name: string; image: string; state: string; status: string; ports: string; createdAt: string; stack?: string; service?: string; cpuPct: number; memUsage: string; memPct: number; netIO: string }
export interface ContainerDetail { id: string; name: string; image: string; state: string; startedAt: string; restartCount: number; restartPolicy: string; cmd: string[]; env: string[]; mounts: { type: string; source: string; destination: string; rw: boolean }[]; ports: Record<string, string>; labels: Record<string, string>; memoryLimit: number; cpuLimit: number; networks: string[]; stack?: string; service?: string }
export interface DockerImage { id: string; repository: string; tag: string; size: string; createdAt: string; dangling: boolean; inUse: boolean }
export interface DockerVolume { name: string; driver: string; mountpoint: string; size: string; inUse: boolean; stack?: string }
export interface DockerNetwork { id: string; name: string; driver: string; scope: string }
export interface Stack { name: string; path: string; managed: boolean; status: string; services: number; updatedAt?: string }
export interface CommandEntry { id: number; actor: string; command: string; exitCode: number; durationMs: number; stderr?: string; createdAt: string }

export interface FileEntry { name: string; path: string; isDir: boolean; size: number; mode: string; perms: string; owner: string; group: string; modTime: string; isSymlink: boolean; target?: string; protected: boolean }
export interface TrashItem { id: string; original: string; name: string; isDir: boolean; size: number; deletedAt: string; actor: string }

export interface ProxyStatus { dnsProvider?: string; installed: boolean; running: boolean; image: string; acmeEmail: string; httpPort: string; httpsPort: string; error?: string; needsRestart?: boolean; problems?: string[] }
export type TargetType = "container" | "panel" | "url";
/** One extra path on a host, forwarded somewhere of its own. */
export interface DomainLocation { id?: string; path: string; targetType: TargetType; target: string; port: number; stripPath: boolean }
export interface Domain { id: string; host: string; targetType: TargetType; target: string; port: number; pathPrefix: string; tls: "letsencrypt" | "letsencrypt-dns" | "self" | "none"; redirectWww: boolean; basicAuth: string; ipAllowlist: string; rateLimit: number; headers: string; maintenance: boolean; protect?: boolean; enabled: boolean; passHost?: boolean; blockExploits?: boolean; locations?: DomainLocation[]; createdAt: string; updatedAt: string }
/** One host found in another proxy's configuration. */
export interface FoundSite {
  hosts: string[]; upstream: string; root: string; rawUpstream?: string; tls: boolean; file: string; source?: string;
  locations?: { path: string; upstream: string; rawUpstream?: string; stripPath?: boolean }[];
  noRoot?: boolean; skipped?: string[];
}
export interface FoundProxy { source: string; files: string[]; sites: FoundSite[] }
/** What importing one host would do, before anything is written. */
export interface ImportProposal {
  host: string; target: string; note: string; source?: string; file?: string; saved: boolean;
  locations?: { path: string; target: string; stripPath: boolean; note?: string }[];
  skipped?: string[];
}

/** A directory, a command, and a session that outlives the browser. */
export interface Workspace {
  id: string; name: string; directory: string;
  preset: "claude" | "shell" | "custom";
  command: string; mcpEnabled: boolean; skipPermissions?: boolean;
  createdAt: string; updatedAt: string; lastAttachedAt: string;
  running: boolean; started?: string; error?: string;
}
export interface WorkspaceMCP { enabled: boolean; path: string; tools: number; scopes: string[] }
/**
 * One program running inside a workspace: a tmux window of its own, and for
 * Claude Code a conversation it keeps returning to. `running` means the command
 * is actually going; `present` only means the window exists, which it also does
 * when the agent has exited and left a prompt behind.
 */
export interface Agent {
  id: string; workspaceId: string; name: string;
  preset: "claude" | "shell" | "custom";
  command: string; resume: boolean; skipPermissions: boolean;
  sessionUuid?: string;
  createdAt: string; updatedAt: string; lastStartedAt: string;
  present: boolean; running: boolean; doing?: string;
}

export interface DNSCheck { host: string; expected: string; resolved: string[]; ok: boolean; proxiedBy?: string; suggestion: string }

export interface CatalogApp { name: string; slug: string; category: string; description: string; website: string; service: string; port: number; fields: { key: string; label: string; type: string; default: string; hint?: string }[]; volumes: string[]; notes: string; needsDomain: boolean; compose?: string }
export interface MailRelay { domain: string; hostname: string; relayhost?: string; installed: boolean; running: boolean; publicIp: string; panelSmtp: string; appSmtp: string; records: { name: string; type: string; value: string; found?: string; ok: boolean; purpose: string }[] }
export interface Recipe { name: string; slug: string; category: string; description: string; time: string; inputs: { key: string; label: string; type?: string; default?: string; hint?: string; optional?: boolean; options?: string }[]; steps: { type: string; label?: string }[]; done: string }
export interface InstalledApp { slug: string; name: string; domain?: string; installedAt: string; values?: Record<string, string> }

export interface Channel { id: string; type: string; name: string; config?: Record<string, string>; categories: string; minSeverity: "info" | "warning" | "critical"; quietFrom: string; quietTo: string; digest?: string; subjects?: string; enabled: boolean; createdAt: string }
export interface IsletEvent { id: number; category: string; severity: "info" | "warning" | "critical"; title: string; message: string; link: string; subject?: string; createdAt: string }

export interface JobRun { id: number; jobId: string; trigger: string; attempt: number; status: string; exitCode: number; output?: string; startedAt: string; finishedAt: string; durationMs: number }
export interface Job {
  id: string; name: string; type: string; schedule: string; timezone: string; command: string; script: string; container: string; httpMethod: string;
  workDir: string; runAs: string; timeoutSec: number; overlap: string; retries: number; nice: number; jitterSec: number; graceSec: number; notifyOn: string; enabled: boolean;
  lastPingAt: string; overdue: boolean; createdAt: string; updatedAt: string; nextRun: string; lastRun?: JobRun; running: boolean; described: string;
}
export interface JobTemplate { id: string; name: string; description: string; schedule: string; script: string }

export interface DBInstance { name: string; slug: string; engine: "postgres" | "mysql" | "redis" | "mongo"; container: string; state: string; image: string; port: number; network: string; public?: string; allowFrom?: string; ip?: string; pooler?: boolean; pooledUrl?: string; user: string; password?: string; rootUser?: string; rootPassword?: string; database?: string; internalUrl: string; publicUrl?: string; installedAt: string }
export interface DBDatabase { name: string; size: string; connections: number; owner?: string }
export interface DBStats { version: string; connections: number; maxConnections: number; uptime: string; dataSize: string; extra?: string[] }
export interface DBExtension { name: string; installed: boolean; available: boolean; comment: string }
export interface DBDump { file: string; database: string; size: number; createdAt: string }
export interface DBDetail extends DBInstance { databases: DBDatabase[]; stats?: DBStats; extensions: DBExtension[]; dumps: DBDump[]; error?: string; dumpJob?: Job }

export interface Check { id: string; name: string; type: "http" | "tcp" | "keyword"; target: string; keyword: string; intervalSec: number; timeoutSec: number; expectStatus: number; enabled: boolean; status: string; failures: number; lastCheckAt: string; lastLatencyMs: number; lastError: string; downSince: string; createdAt: string; uptime24h: number; uptime30d: number }
export interface CheckResult { at: string; ok: boolean; latencyMs: number; error?: string }
export interface LogSource { id: string; label: string; group: string }

export interface Release { id: number; appId: string; number: number; trigger: string; actor: string; commit: string; message: string; author: string; status: string; image: string; container: string; log?: string; error: string; startedAt: string; finishedAt: string; durationMs: number }
export interface DeployApp {
  id: string; name: string; source: "git" | "image" | "upload"; repoUrl: string; branch: string; rootDir: string; image: string; strategy: string; framework: string;
  installCmd: string; buildCmd: string; startCmd: string; outputDir: string; port: number; healthPath: string; predeployCmd: string; env: string; domain: string; tls: string;
  webhookSecret?: string; autoDeploy: boolean; memoryMb: number; cpus: number; volumes: string; processes: string; deployOn: "push" | "ci"; ioMbps?: number; processList?: { name: string; count: number; cmd: string }[]; currentRelease: number; status: string; createdAt: string; updatedAt: string;
  nodeVersion: string; pythonVersion: string; url: string; container: string; deploying: boolean; lastRelease?: Release;
}
export interface Detection { strategy: string; framework: string; summary: string; installCmd: string; buildCmd: string; startCmd: string; outputDir: string; port: number; healthPath: string; composeFile?: string; nodeVersion?: string; pythonVersion?: string }

export interface ApiToken { id: string; userId: string; name: string; scopes: string; lastUsedAt: string; expiresAt: string; createdAt: string; prefix?: string }

export interface RunnerPool { id: string; provider: string; name: string; url: string; token?: string; labels: string; minIdle: number; maxRunners: number; dockerAccess: boolean; memoryMb: number; cpus: number; webhookSecret?: string; enabled: boolean; createdAt: string; runners: { name: string; state: string; busy: boolean; started: string }[]; idle: number; busy: number; error?: string }
export interface RunnerJob { id: number; poolId: string; externalId: string; name: string; repo: string; runner: string; status: string; conclusion: string; url: string; queuedAt: string; startedAt: string; finishedAt: string }

export interface SecCheck { id: string; title: string; detail: string; weight: number; status: "pass" | "fail" | "warn" | "unknown"; fix?: string; fixNote?: string }
export interface SecReport { score: number; max: number; checks: SecCheck[]; linux: boolean; computedAt: string }
export interface FirewallRule { port: string; proto: string; from: string; comment: string; routed?: boolean }
export interface SSHSettings { port: number; permitRootLogin: boolean; passwordAuth: boolean; pubkeyAuth: boolean; maxAuthTries: number; allowAgentForwarding: boolean; x11Forwarding: boolean; clientAliveCountMax: number }
export interface HostAudit { at: string; suid: string[]; worldWritable: string[]; etcChanged: string[]; etcAdded: string[]; etcRemoved: string[]; baselineAt: string; rkhunter: string; rkhunterRan: boolean; notes: string[] }
export interface Scan { target: string; at: string; critical: number; high: number; medium: number; low: number; findings: { id: string; package: string; version: string; fixed: string; severity: string; title: string }[]; error?: string; truncated?: boolean }
export interface SecurityState { panelCidr?: string; report: SecReport; firewall: { installed: boolean; active: boolean; rules: FirewallRule[]; dockerAware: boolean; missingRoutes?: string[] }; ssh: SSHSettings; sshHasKeys: boolean; sshRollback: boolean; banned: string[]; scans: Scan[]; clientIp: string }

export interface BackupDestination { id: string; name: string; type: "s3" | "sftp" | "local" | "rest"; config: Record<string, string>; password?: string; lastCheck: string; checkOk: boolean; size: number; lastRestoreTest: string; restoreTestOk: boolean; createdAt: string; repo: string }
export interface BackupSource { type: "volume" | "path" | "database" | "islet"; value: string }
export interface BackupPlan { id: string; name: string; destinationId: string; sources: BackupSource[]; schedule: string; keepDaily: number; keepWeekly: number; keepMonthly: number; keepYearly: number; enabled: boolean; preCmd?: string; postCmd?: string; pause?: boolean; nextRunAt: string; lastRunAt: string; lastStatus: string; createdAt: string; described: string; running: boolean; stale: boolean }
export interface BackupRun { id: number; planId: string; trigger: string; status: string; snapshot: string; filesNew: number; filesChanged: number; bytesAdded: number; bytesTotal: number; log?: string; error: string; startedAt: string; finishedAt: string; durationMs: number }
export interface Snapshot { id: string; time: string; paths: string[]; tags: string[]; size: number }
export interface Registry { host: string; username: string }
export interface BackupHost { domain: string; tls: string; user: string; password?: string; url: string }
export interface BackupOverview { kitDownloadedAt?: string; destinations: BackupDestination[]; plans: BackupPlan[]; health: { plans: number; destinations: number; lastSuccess: string; nextRun: string; stale: number; failed: number; lastVerified: string; lastRestoreTest: string; size: number }; volumes: string[]; databases?: string[] }

export interface Attention { securityScore: number; securityFailing: number; backups?: { plans: number; destinations: number; lastSuccess: string; nextRun: string; stale: number; failed: number; lastVerified: string }; checksDown: string[]; deploysFailed: string[]; jobsFailed: string[]; apps?: number; criticals: { title: string; at: string; link: string }[] }

export interface GitHubRepo { fullName: string; defaultBranch: string; private: boolean; url: string; installation: number }
export interface GitHubState { config: { appId: string; clientId: string; slug: string; configured: boolean }; hookUrl: string; installations?: { id: number; account: string; type: string }[]; error?: string }

export class RequestError extends Error {
  status: number;
  body: ApiError;
  constructor(status: number, body: ApiError) {
    super(body.message || `request failed with ${status}`);
    this.status = status;
    this.body = body;
  }
}

// The session ending is not an error one page can handle: it ends every page at
// once. AuthProvider registers here so a 401 anywhere returns the whole panel to
// the sign-in screen instead of leaving stale data on screen, polling forever.
let unauthorized: (() => void) | null = null;
let lastUnauthorized = 0;

export function onSessionExpired(fn: () => void) {
  unauthorized = fn;
}

function onUnauthorized() {
  // Many requests are usually in flight when a session ends; one is enough.
  if (Date.now() - lastUnauthorized < 3000) return;
  lastUnauthorized = Date.now();
  unauthorized?.();
}

// Which server the panel is pointed at.
//
// "local" is the machine this panel runs on. Anything else is a managed server,
// and every request is forwarded to it through the panel's SSH tunnel. Doing it
// here rather than in each page is the whole trick: a page asks for
// /api/v1/domains and does not need to know which machine answers.
const SERVER_KEY = "islet.server";
let currentServer = (() => {
  try { return sessionStorage.getItem(SERVER_KEY) || "local"; } catch { return "local"; }
})();
const serverListeners = new Set<(id: string) => void>();

export function getServer() { return currentServer; }

export function setServer(id: string) {
  if (id === currentServer) return;
  currentServer = id || "local";
  try { sessionStorage.setItem(SERVER_KEY, currentServer); } catch { /* private mode */ }
  serverListeners.forEach((fn) => fn(currentServer));
}

export function onServerChange(fn: (id: string) => void): () => void {
  serverListeners.add(fn);
  return () => { serverListeners.delete(fn); };
}

/** Rewrite an API path for the server in view. Exported for streams and links. */
export function apiPath(path: string): string {
  if (currentServer === "local" || !path.startsWith("/api/v1/")) return path;
  // Who you are stays here: your session, your password and your second factor
  // are about this panel and follow you across every server you look at. So
  // does the list of servers itself.
  //
  // Users do not. A managed server keeps its own accounts, and those are the
  // ones that matter the moment it serves a panel on its own domain or
  // protects a site with an Islet login — both ask that server who you are,
  // not this one. Without this you can add a machine to the fleet, give it a
  // domain, and then be locked out of the panel you just published.
  //
  // Everything else follows the selection too, including updates: opening
  // Settings while a managed server is in view and updating it there is the
  // point, not an accident.
  const local = ["/api/v1/auth/", "/api/v1/servers", "/api/v1/setup"];
  if (local.some((p) => path.startsWith(p))) return path;
  const [head, query] = path.slice("/api/v1/".length).split("?");
  return `/api/v1/servers/${currentServer}/proxy/${head}` + (query ? `?${query}` : "");
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(apiPath(path), {
    ...init,
    credentials: "same-origin",
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let body: ApiError = { error: "http_error", message: res.statusText };
    try { body = (await res.json()) as ApiError; } catch { /* keep default */ }
    if (res.status === 401) onUnauthorized();
    throw new RequestError(res.status, body);
  }
  // Not every success carries a body: 204 for a delete, 202 for work that has
  // only been started. Parsing those as JSON throws a syntax error that then
  // gets shown to the person as if the request had failed.
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

function post<T>(path: string, body?: unknown, method = "POST"): Promise<T> {
  return request<T>(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export const api = {
  health: () => request<Health>("/api/v1/health"),
  setupStatus: () => request<{ needsSetup: boolean; cookieDomain?: string }>("/api/v1/setup"),
  setup: (token: string, username: string, password: string) =>
    post<LoginResponse>("/api/v1/setup", { token, username, password }),
  login: (username: string, password: string) => post<LoginResponse>("/api/v1/auth/login", { username, password }),
  mfa: (code: string) => post<LoginResponse>("/api/v1/auth/mfa", { code }),
  logout: () => post<void>("/api/v1/auth/logout"),
  me: () => request<Me>("/api/v1/auth/me"),
  changePassword: (current: string, next: string) => post<void>("/api/v1/auth/password", { current, new: next }),
  totpSetup: () => post<{ secret: string; otpauthUrl: string }>("/api/v1/auth/totp/setup"),
  totpEnable: (code: string) => post<{ codes: string[] }>("/api/v1/auth/totp/enable", { code }),
  totpDisable: (code: string) => post<void>("/api/v1/auth/totp/disable", { code }),
  users: () => request<User[]>("/api/v1/users"),
  userCreate: (b: { username: string; password: string; role: string }) => post<User>("/api/v1/users", b),
  userUpdate: (id: string, b: { role: string; password: string; projects?: string }) => post<void>(`/api/v1/users/${id}`, b, "PUT"),
  userDelete: (id: string) => post<void>(`/api/v1/users/${id}`, undefined, "DELETE"),
  attention: () => request<Attention>("/api/v1/attention"),
  portCheck: (host: string, port: number) => request<{ open: boolean; message: string }>(`/api/v1/diagnostics?tool=port&host=${encodeURIComponent(host)}&port=${port}`),
  cookieDomain: () => request<{ cookieDomain: string }>("/api/v1/auth/cookie-domain"),
  cookieDomainSet: (cookieDomain: string) => post<{ cookieDomain: string }>("/api/v1/auth/cookie-domain", { cookieDomain }),
  tokens: () => request<ApiToken[]>("/api/v1/auth/tokens"),
  tokenCreate: (b: { name: string; scopes: string; ttlDays: number }) => post<{ token: string; info: ApiToken }>("/api/v1/auth/tokens", b),
  tokenRevoke: (id: string) => post<void>(`/api/v1/auth/tokens/${id}`, undefined, "DELETE"),
  sessions: () => request<Session[]>("/api/v1/auth/sessions"),
  revokeSession: (id: string) => post<void>(`/api/v1/auth/sessions/${id}`, undefined, "DELETE"),
  system: () => request<HostInfo>("/api/v1/system"),
  processes: (limit = 15) => request<Process[]>(`/api/v1/system/processes?limit=${limit}`),
  ports: () => request<Port[]>("/api/v1/system/ports"),
  metricsLatest: () => request<{ latest: Sample | null; recent: Sample[] }>("/api/v1/metrics/latest"),
  audit: (limit = 50, before?: number) => request<AuditEntry[]>(`/api/v1/audit?limit=${limit}${before ? `&before=${before}` : ""}`),
  updateCheck: (channel: "stable" | "beta" = "stable") => request<UpdateStatus>(`/api/v1/system/update?channel=${channel}`),
  updateApply: (channel: "stable" | "beta" = "stable") => post<{ from: string; to: string }>(`/api/v1/system/update?channel=${channel}`),
  filesList: (path: string) => request<{ path: string; entries: FileEntry[]; protected: boolean }>(`/api/v1/files?path=${encodeURIComponent(path)}`),
  filesRead: (path: string) => request<{ path: string; content: string; size: number }>(`/api/v1/files/read?path=${encodeURIComponent(path)}`),
  filesTail: (path: string, n = 65536) => request<{ path: string; content: string; size: number }>(`/api/v1/files/tail?path=${encodeURIComponent(path)}&bytes=${n}`),
  filesWrite: (path: string, content: string) => post<void>("/api/v1/files/write", { path, content }, "PUT"),
  filesOp: (body: Record<string, unknown>) => post<unknown>("/api/v1/files/op", body),
  filesSearch: (root: string, q: string, content: boolean) => request<{ path: string; isDir: boolean; line?: number; text?: string }[]>(`/api/v1/files/search?root=${encodeURIComponent(root)}&q=${encodeURIComponent(q)}&content=${content ? 1 : 0}`),
  filesUsage: (path: string) => request<{ path: string; total: number; truncated: boolean; children: { name: string; size: number; isDir: boolean }[] }>(`/api/v1/files/usage?path=${encodeURIComponent(path)}`),
  trash: () => request<TrashItem[]>("/api/v1/files/trash"),
  trashOp: (body: { op: "restore" | "purge"; id: string }) => post<unknown>("/api/v1/files/trash", body),
  diskReport: () => request<{ key: string; label: string; bytes: number; reclaimable: number; hint: string; cleanable: boolean }[]>("/api/v1/system/disk"),
  diskClean: (keys: string[]) => post<Record<string, string>>("/api/v1/system/disk/clean", { keys }),
  proxyStatus: () => request<ProxyStatus>("/api/v1/proxy"),
  proxyInstall: (acmeEmail: string, dns?: { dnsProvider: string; dnsEnv: Record<string, string> }) => post<ProxyStatus>("/api/v1/proxy/install", { acmeEmail, ...(dns ?? {}) }),
  dnsProviders: () => request<Record<string, string[]>>("/api/v1/proxy/dns-providers"),
  proxyRemove: () => post<void>("/api/v1/proxy/remove"),
  proxyCerts: () => request<{ domain: string; sans: string[]; notAfter: string; issuer: string }[]>("/api/v1/proxy/certs"),
  previewHost: (name: string) => request<{ host: string; publicIp: string }>(`/api/v1/proxy/preview-host?name=${encodeURIComponent(name)}`),
  domains: () => request<Domain[]>("/api/v1/domains"),
  domainSave: (d: Domain) => d.id ? post<Domain>(`/api/v1/domains/${d.id}`, d, "PUT") : post<Domain>("/api/v1/domains", d),
  domainDelete: (id: string) => post<void>(`/api/v1/domains/${id}`, undefined, "DELETE"),
  domainDns: (id: string) => request<DNSCheck>(`/api/v1/domains/${id}/dns`),
  dnsCheck: (host: string) => request<DNSCheck>(`/api/v1/dns-check?host=${encodeURIComponent(host)}`),
  catalog: () => request<CatalogApp[]>("/api/v1/catalog"),
  catalogApp: (slug: string) => request<CatalogApp>(`/api/v1/catalog/${slug}`),
  installedUpdates: () => request<Record<string, string[]>>("/api/v1/catalog/installed/updates"),
  stackImport: (name: string) => post<{ name: string; from: string; note: string }>("/api/v1/docker/stacks/import", { name }),
  github: () => request<GitHubState>("/api/v1/github"),
  githubSave: (b: { appId: string; clientId: string; slug: string; privateKey: string; webhookSecret: string }) => post<void>("/api/v1/github", b),
  githubRepos: () => request<GitHubRepo[]>("/api/v1/github/repos"),
  /** What the sidebar shows: links somebody added, plus catalog apps with a domain. */
  sidebar: () => request<{ label: string; url: string; auto?: boolean }[]>("/api/v1/sidebar"),
  /** Only the links the editor may change; the automatic ones are left out. */
  sidebarManual: () => request<{ label: string; url: string }[]>("/api/v1/sidebar?manual=1"),
  sidebarSet: (links: { label: string; url: string }[]) => post<{ label: string; url: string }[]>("/api/v1/sidebar", links),
  geo: () => request<{ enabled: boolean }>("/api/v1/auth/geo"),
  geoSet: (enabled: boolean) => post<{ enabled: boolean }>("/api/v1/auth/geo", { enabled }),
  dbAdminer: (name: string, setup?: { host: string; tls: string; protect: boolean }) => post<{ url: string; installed: boolean; password?: string; domain: string }>(`/api/v1/databases/${encodeURIComponent(name)}/adminer`, setup),
  weeklyReport: () => request<{ enabled: boolean; lastSent: string }>("/api/v1/report/weekly"),
  weeklyReportSet: (enabled: boolean) => post<{ enabled: boolean; lastSent: string }>("/api/v1/report/weekly", { enabled }),
  weeklyReportSend: () => post<{ body: string }>("/api/v1/report/weekly/send"),
  mcp: () => request<{ enabled: boolean; url: string }>("/api/v1/mcp"),
  mcpSet: (enabled: boolean) => post<{ enabled: boolean }>("/api/v1/mcp", { enabled }),
  mailRelay: () => request<MailRelay>("/api/v1/mail/relay"),
  mailRelaySet: (b: { domain: string; hostname: string; relayhost?: string; relayUser?: string; relayPassword?: string }) => post<MailRelay>("/api/v1/mail/relay", b),
  mailRelayRemove: () => post<void>("/api/v1/mail/relay", undefined, "DELETE"),
  mailRelayTest: (to: string) => post<{ status: string; queue: string }>("/api/v1/mail/relay/test", { to }),
  catalogSource: () => request<{ source: { url: string; fetchedAt: string; apps: number; recipes: number; lastError?: string }; default: string }>("/api/v1/catalog/source"),
  catalogSourceSet: (url: string) => post<{ source: { url: string; fetchedAt: string; apps: number; recipes: number; lastError?: string }; default: string }>("/api/v1/catalog/source", { url }),
  catalogSourceClear: () => post<void>("/api/v1/catalog/source", undefined, "DELETE"),
  recipes: () => request<Recipe[]>("/api/v1/recipes"),
  installedApps: () => request<InstalledApp[]>("/api/v1/catalog/installed"),
  envGroups: () => request<{ name: string; keys: string[]; env?: string }[]>("/api/v1/apps/env-groups"),
  envGroupSave: (name: string, env: string) => post<void>("/api/v1/apps/env-groups", { name, env }),
  envGroupDelete: (name: string) => post<void>(`/api/v1/apps/env-groups/${name}`, undefined, "DELETE"),
  securityFixAll: () => post<{ fix: string; status: string; error?: string }[]>("/api/v1/security/fix-all"),
  deployApps: () => request<DeployApp[]>("/api/v1/apps"),
  deployApp: (id: string) => request<DeployApp>(`/api/v1/apps/${id}`),
  deployAppSave: (a: Partial<DeployApp>) => a.id ? post<DeployApp>(`/api/v1/apps/${a.id}`, a, "PUT") : post<DeployApp>("/api/v1/apps", a),
  deployAppDelete: (id: string) => post<void>(`/api/v1/apps/${id}`, undefined, "DELETE"),
  deployInspect: (repoUrl: string, branch: string, rootDir: string) => post<Detection>("/api/v1/apps/inspect", { repoUrl, branch, rootDir }),
  deployCancel: (id: string) => post<void>(`/api/v1/apps/${id}/cancel`),
  releases: (id: string) => request<Release[]>(`/api/v1/apps/${id}/releases`),
  release: (id: string, rel: number) => request<Release>(`/api/v1/apps/${id}/releases/${rel}`),
  backups: () => request<BackupOverview>("/api/v1/backups"),
  destinationSave: (d: Partial<BackupDestination>) => d.id ? post<BackupDestination>(`/api/v1/backups/destinations/${d.id}`, d, "PUT") : post<BackupDestination>("/api/v1/backups/destinations", d),
  destinationDelete: (id: string) => post<void>(`/api/v1/backups/destinations/${id}`, undefined, "DELETE"),
  destinationRestoreTest: (id: string) => post<{ output: string }>(`/api/v1/backups/destinations/${id}/restore-test`),
  destinationVerify: (id: string) => post<{ output: string }>(`/api/v1/backups/destinations/${id}/verify`),
  snapshots: (id: string, plan?: string) => request<Snapshot[]>(`/api/v1/backups/destinations/${id}/snapshots${plan ? `?plan=${encodeURIComponent(plan)}` : ""}`),
  snapshotLs: (id: string, snap: string, path: string) => request<{ path: string; name: string; type: string; size?: number; mtime?: string }[]>(`/api/v1/backups/destinations/${id}/snapshots/${snap}/ls?path=${encodeURIComponent(path)}`),
  restore: (id: string, b: { snapshot: string; include: string; newVolume: string; dryRun?: boolean }) => post<{ target: string; dryRun: boolean }>(`/api/v1/backups/destinations/${id}/restore`, b),
  restoreDatabase: (id: string, b: { snapshot: string; path: string; slug?: string; newInstance?: string }) => post<{ instance: string; database: string; internalUrl: string }>(`/api/v1/backups/destinations/${id}/restore-database`, b),
  backupHost: () => request<BackupHost>("/api/v1/backups/host"),
  backupHostSet: (domain: string, tls: string) => post<BackupHost>("/api/v1/backups/host", { domain, tls }),
  backupHostRemove: () => post<void>("/api/v1/backups/host", undefined, "DELETE"),
  planSave: (p: Partial<BackupPlan>) => p.id ? post<BackupPlan>(`/api/v1/backups/plans/${p.id}`, p, "PUT") : post<BackupPlan>("/api/v1/backups/plans", p),
  planDelete: (id: string) => post<void>(`/api/v1/backups/plans/${id}`, undefined, "DELETE"),
  planCancel: (id: string) => post<void>(`/api/v1/backups/plans/${id}/cancel`),
  planRuns: (id: string) => request<BackupRun[]>(`/api/v1/backups/plans/${id}/runs`),
  planRun: (id: string, run: number) => request<BackupRun>(`/api/v1/backups/plans/${id}/runs/${run}`),
  registries: () => request<Registry[]>("/api/v1/docker/registries"),
  registryLogin: (b: { host: string; username: string; password: string }) => post<void>("/api/v1/docker/registries", b),
  registryLogout: (host: string) => post<void>(`/api/v1/docker/registries/${host}`, undefined, "DELETE"),
  panelRestrict: (cidr: string) => post<void>("/api/v1/security/firewall/panel", { cidr }),
  lynis: () => post<{ score: number; output: string }>("/api/v1/security/lynis"),
  security: () => request<SecurityState>("/api/v1/security"),
  securityFix: (id: string) => post<{ output: string }>(`/api/v1/security/fix/${id}`),
  firewallAllow: (b: { port: string; proto: string; from: string; comment: string; routed: boolean }) => post<void>("/api/v1/security/firewall/rules", b),
  firewallDelete: (b: { port: string; proto: string; from: string; routed: boolean }) => post<void>("/api/v1/security/firewall/rules", b, "DELETE"),
  unban: (ip: string) => post<void>("/api/v1/security/unban", { ip }),
  sshApply: (cfg: SSHSettings) => post<{ message: string }>("/api/v1/security/ssh", cfg),
  sshConfirm: () => post<void>("/api/v1/security/ssh/confirm"),
  hostUser: (name: string, publicKey: string) => post<{ output: string }>("/api/v1/security/host/user", { name, publicKey }),
  hostSSHKey: (user: string, publicKey: string) => post<{ output: string }>("/api/v1/security/host/sshkey", { user, publicKey }),
  hostTimezone: () => request<{ timezone: string }>("/api/v1/security/host/timezone"),
  hostTimezoneSet: (timezone: string) => post<{ timezone: string }>("/api/v1/security/host/timezone", { timezone }),
  hostAudit: () => request<HostAudit | null>("/api/v1/security/host/audit"),
  hostAuditRun: (rkhunter: boolean) => post<HostAudit>("/api/v1/security/host/audit", { rkhunter }),
  hostBaseline: () => post<{ files: number }>("/api/v1/security/host/baseline"),
  scanImage: (image: string) => post<Scan>("/api/v1/security/scan", { image }),
  panic: () => post<{ output: string; message: string }>("/api/v1/security/panic"),
  runnerPools: () => request<RunnerPool[]>("/api/v1/runners"),
  runnerPoolSave: (p: Partial<RunnerPool>) => p.id ? post<RunnerPool>(`/api/v1/runners/${p.id}`, p, "PUT") : post<RunnerPool>("/api/v1/runners", p),
  runnerPoolDelete: (id: string) => post<void>(`/api/v1/runners/${id}`, undefined, "DELETE"),
  runnerJobs: (id: string) => request<RunnerJob[]>(`/api/v1/runners/${id}/jobs`),
  runnerWorkflow: async (id: string, app: string) => { const r = await fetch(`/api/v1/runners/${id}/workflow?app=${encodeURIComponent(app)}`, { credentials: "same-origin" }); return r.text(); },
  checks: () => request<Check[]>("/api/v1/uptime/checks"),
  checkSave: (c: Partial<Check>) => c.id ? post<Check>(`/api/v1/uptime/checks/${c.id}`, c, "PUT") : post<Check>("/api/v1/uptime/checks", c),
  checkDelete: (id: string) => post<void>(`/api/v1/uptime/checks/${id}`, undefined, "DELETE"),
  checkResults: (id: string, limit = 120) => request<CheckResult[]>(`/api/v1/uptime/checks/${id}/results?limit=${limit}`),
  checkProbe: (id: string) => post<CheckResult>(`/api/v1/uptime/checks/${id}/probe`),
  logSources: () => request<LogSource[]>("/api/v1/logs/sources"),
  databases: () => request<DBInstance[]>("/api/v1/databases"),
  database: (name: string) => request<DBDetail>(`/api/v1/databases/${name}`),
  dbCreate: (name: string, b: { name: string; user: string; password: string }) => post<{ url: string }>(`/api/v1/databases/${name}/databases`, b),
  dbDrop: (name: string, db: string) => post<void>(`/api/v1/databases/${name}/databases/${db}`, undefined, "DELETE"),
  dbSlow: (name: string) => request<{ query: string; calls: number; meanMs: number }[]>(`/api/v1/databases/${name}/slow`),
  dbExtension: (name: string, ext: string, enabled: boolean) => post<void>(`/api/v1/databases/${name}/extensions`, { name: ext, enabled }),
  dbDump: (name: string, database: string) => post<DBDump>(`/api/v1/databases/${name}/dumps`, { database }),
  dbRestore: (name: string, file: string, database: string) => post<void>(`/api/v1/databases/${name}/restore`, { file, database }),
  dbDumpDelete: (name: string, file: string) => post<void>(`/api/v1/databases/${name}/dumps/${file}`, undefined, "DELETE"),
  dbSchedule: (name: string, b: { schedule: string; keepDays: number; enabled: boolean }) => post<Job>(`/api/v1/databases/${name}/schedule`, b),
  jobs: () => request<Job[]>("/api/v1/cron/jobs"),
  job: (id: string) => request<Job>(`/api/v1/cron/jobs/${id}`),
  jobSave: (j: Partial<Job>) => j.id ? post<Job>(`/api/v1/cron/jobs/${j.id}`, j, "PUT") : post<Job>("/api/v1/cron/jobs", j),
  jobDelete: (id: string) => post<void>(`/api/v1/cron/jobs/${id}`, undefined, "DELETE"),
  jobKill: (id: string) => post<void>(`/api/v1/cron/jobs/${id}/kill`),
  jobRuns: (id: string) => request<JobRun[]>(`/api/v1/cron/jobs/${id}/runs`),
  jobRun: (id: string, run: number) => request<JobRun>(`/api/v1/cron/jobs/${id}/runs/${run}`),
  jobVersions: (id: string) => request<{ id: number; actor: string; createdAt: string }[]>(`/api/v1/cron/jobs/${id}/versions`),
  jobVersion: (id: string, v: number) => request<{ id: number; content: string }>(`/api/v1/cron/jobs/${id}/versions/${v}`),
  jobExport: (id: string) => request<{ crontab: string; scriptPath: string }>(`/api/v1/cron/jobs/${id}/export`),
  cronPreview: (schedule: string, timezone: string) => post<{ described: string; next: string[] }>("/api/v1/cron/preview", { schedule, timezone }),
  cronTemplates: () => request<JobTemplate[]>("/api/v1/cron/templates"),
  cronLint: (script: string) => post<{ available: boolean; output: string }>("/api/v1/cron/lint", { script }),
  cronImport: (text: string, save: boolean, source = "crontab") => post<Job[]>("/api/v1/cron/import", { text, save, source }),
  /** Everything a known reverse proxy is already serving on this server. */
  importScan: () => request<{ found: FoundProxy[] }>("/api/v1/domains/import/scan"),
  importSites: (b: { text: string; save: boolean; hosts?: string[] }) =>
    post<ImportProposal[]>("/api/v1/domains/import", { text: b.text, save: b.save, hosts: b.hosts ?? [] }),
  workspaces: () => request<{ workspaces: Workspace[]; tmux: boolean; claude: boolean; claudePath: string }>("/api/v1/workspaces"),
  workspaceSave: (w: Workspace) => w.id
    ? post<Workspace>(`/api/v1/workspaces/${w.id}`, w, "PUT")
    : post<Workspace>("/api/v1/workspaces", w),
  workspaceDelete: (id: string) => post<void>(`/api/v1/workspaces/${id}`, undefined, "DELETE"),
  workspaceStart: (id: string) => post<Workspace>(`/api/v1/workspaces/${id}/start`),
  workspaceStop: (id: string) => post<Workspace>(`/api/v1/workspaces/${id}/stop`),
  workspaceHistory: (id: string) => request<{ text: string }>(`/api/v1/workspaces/${id}/history`),
  workspaceMcp: (id: string) => request<WorkspaceMCP>(`/api/v1/workspaces/${id}/mcp`),
  workspaceMcpRenew: (id: string) => post<WorkspaceMCP>(`/api/v1/workspaces/${id}/mcp`),
  agents: (ws: string) => request<Agent[]>(`/api/v1/workspaces/${ws}/agents`),
  agentSave: (ws: string, a: Partial<Agent>) => a.id
    ? post<Agent>(`/api/v1/workspaces/${ws}/agents/${a.id}`, a, "PUT")
    : post<Agent>(`/api/v1/workspaces/${ws}/agents`, a),
  agentDelete: (ws: string, id: string) => post<void>(`/api/v1/workspaces/${ws}/agents/${id}`, undefined, "DELETE"),
  agentStart: (ws: string, id: string) => post<Agent>(`/api/v1/workspaces/${ws}/agents/${id}/start`),
  agentStop: (ws: string, id: string) => post<Agent>(`/api/v1/workspaces/${ws}/agents/${id}/stop`),
  channels: () => request<Channel[]>("/api/v1/notify/channels"),
  channelSave: (c: Channel) => c.id ? post<Channel>(`/api/v1/notify/channels/${c.id}`, c, "PUT") : post<Channel>("/api/v1/notify/channels", c),
  channelDelete: (id: string) => post<void>(`/api/v1/notify/channels/${id}`, undefined, "DELETE"),
  channelTest: (id: string) => post<void>(`/api/v1/notify/channels/${id}/test`),
  telegramDetect: (token: string) => post<{ chatId: string; name: string; type: string }[]>("/api/v1/notify/telegram/detect", { token }),
  events: (limit = 50, before?: number) => request<IsletEvent[]>(`/api/v1/notify/events?limit=${limit}${before ? `&before=${before}` : ""}`),
  commands: (limit = 100) => request<CommandEntry[]>(`/api/v1/commands?limit=${limit}`),
  dockerStatus: () => request<DockerStatus>("/api/v1/docker/status"),
  containers: () => request<Container[]>("/api/v1/docker/containers"),
  container: (id: string) => request<ContainerDetail>(`/api/v1/docker/containers/${id}`),
  containerAction: (id: string, action: string) => post<void>(`/api/v1/docker/containers/${id}/${action}`),
  containerLimits: (id: string, body: { memoryBytes: number; cpus: number; restart: string }) => post<void>(`/api/v1/docker/containers/${id}/limits`, body),
  images: () => request<DockerImage[]>("/api/v1/docker/images"),
  imageRemove: (id: string) => post<void>(`/api/v1/docker/images/${id}`, undefined, "DELETE"),
  volumes: () => request<DockerVolume[]>("/api/v1/docker/volumes"),
  volumeRemove: (name: string) => post<void>(`/api/v1/docker/volumes/${name}`, undefined, "DELETE"),
  networks: () => request<DockerNetwork[]>("/api/v1/docker/networks"),
  networkRemove: (name: string) => post<void>(`/api/v1/docker/networks/${name}`, undefined, "DELETE"),
  dockerDF: () => request<{ type: string; total: number; active: number; size: string; reclaimable: string }[]>("/api/v1/docker/df"),
  dockerPrune: (o: { containers: boolean; images: boolean; allImages: boolean; volumes: boolean; networks: boolean; builder: boolean }) => post<Record<string, string>>("/api/v1/docker/prune", o),
  stacks: () => request<Stack[]>("/api/v1/docker/stacks"),
  stack: (name: string) => request<{ name: string; compose: string; env: string }>(`/api/v1/docker/stacks/${name}`),
  stackWrite: (name: string, compose: string, env: string, isNew: boolean) => isNew ? post<{ name: string }>("/api/v1/docker/stacks", { name, compose, env }) : post<{ name: string }>(`/api/v1/docker/stacks/${name}`, { name, compose, env }, "PUT"),
  stackRemove: (name: string, volumes: boolean) => post<void>(`/api/v1/docker/stacks/${name}?volumes=${volumes ? 1 : 0}`, undefined, "DELETE"),
  servers: () => request<{ local: FleetLocal; servers: FleetServer[] }>("/api/v1/servers"),
  serverKey: () => request<{ publicKey: string }>("/api/v1/servers/key"),
  serverAdd: (b: { name: string; host: string; sshUser: string; sshPort: number }) => post<FleetServer>("/api/v1/servers", b),
  serverJoin: (id: string, b: { user: string; password: string; privateKey: string; passphrase: string }) => post<void>(`/api/v1/servers/${id}/join`, b),
  serverForget: (id: string) => post<void>(`/api/v1/servers/${id}`, undefined, "DELETE"),
  serverExposure: (id: string) => request<{ panelOpen: boolean }>(`/api/v1/servers/${id}/exposure`),
  // Closing the port runs on that server, through its own audited firewall
  // route, so it is recorded there like any other rule change.
  serverClosePanel: (id: string, port: number) =>
    post<void>(`/api/v1/servers/${id}/proxy/security/firewall/rules`, { port: String(port), proto: "tcp" }, "DELETE"),
  serverCheck: (id: string) => post<{ ok: boolean; error?: string; server?: FleetServer }>(`/api/v1/servers/${id}/check`),
  metricsHistory: (range: "1h" | "6h" | "24h" | "7d") => request<{ stepSeconds: number; points: Point[] }>(`/api/v1/metrics/history?range=${range}`),
};
