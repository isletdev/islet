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
  diskUsed: number; diskTotal: number; netRx: number; netTx: number;
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
export interface Port { proto: string; address: string; port: number; pid: number; process: string }

export interface AuditEntry { id: number; actor: string; action: string; target: string; detail: string; createdAt: string }
export interface UpdateStatus { current: string; channel: string; latest: string; prerelease: boolean; publishedAt: string; updateAvailable: boolean; notes?: string }

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

export interface ProxyStatus { installed: boolean; running: boolean; image: string; acmeEmail: string; httpPort: string; httpsPort: string; error?: string }
export interface Domain { id: string; host: string; targetType: "container" | "panel" | "url"; target: string; port: number; pathPrefix: string; tls: "letsencrypt" | "self" | "none"; redirectWww: boolean; basicAuth: string; ipAllowlist: string; rateLimit: number; headers: string; maintenance: boolean; enabled: boolean; createdAt: string; updatedAt: string }
export interface DNSCheck { host: string; expected: string; resolved: string[]; ok: boolean; suggestion: string }

export interface CatalogApp { name: string; slug: string; category: string; description: string; website: string; service: string; port: number; fields: { key: string; label: string; type: string; default: string; hint?: string }[]; volumes: string[]; notes: string; needsDomain: boolean; compose?: string }
export interface InstalledApp { slug: string; name: string; domain?: string; installedAt: string; values?: Record<string, string> }

export interface Channel { id: string; type: string; name: string; config?: Record<string, string>; categories: string; minSeverity: "info" | "warning" | "critical"; quietFrom: string; quietTo: string; enabled: boolean; createdAt: string }
export interface IsletEvent { id: number; category: string; severity: "info" | "warning" | "critical"; title: string; message: string; link: string; createdAt: string }

export interface JobRun { id: number; jobId: string; trigger: string; attempt: number; status: string; exitCode: number; output?: string; startedAt: string; finishedAt: string; durationMs: number }
export interface Job {
  id: string; name: string; type: string; schedule: string; timezone: string; command: string; script: string; container: string; httpMethod: string;
  workDir: string; runAs: string; timeoutSec: number; overlap: string; retries: number; nice: number; jitterSec: number; graceSec: number; notifyOn: string; enabled: boolean;
  lastPingAt: string; overdue: boolean; createdAt: string; updatedAt: string; nextRun: string; lastRun?: JobRun; running: boolean; described: string;
}
export interface JobTemplate { id: string; name: string; description: string; schedule: string; script: string }

export interface DBInstance { name: string; slug: string; engine: "postgres" | "mysql" | "redis" | "mongo"; container: string; state: string; image: string; port: number; network: string; public?: string; user: string; password?: string; rootUser?: string; rootPassword?: string; database?: string; internalUrl: string; publicUrl?: string; installedAt: string }
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
  id: string; name: string; source: "git" | "image"; repoUrl: string; branch: string; rootDir: string; image: string; strategy: string; framework: string;
  installCmd: string; buildCmd: string; startCmd: string; outputDir: string; port: number; healthPath: string; predeployCmd: string; env: string; domain: string; tls: string;
  webhookSecret?: string; autoDeploy: boolean; memoryMb: number; cpus: number; volumes: string; currentRelease: number; status: string; createdAt: string; updatedAt: string;
  nodeVersion: string; pythonVersion: string; url: string; container: string; deploying: boolean; lastRelease?: Release;
}
export interface Detection { strategy: string; framework: string; summary: string; installCmd: string; buildCmd: string; startCmd: string; outputDir: string; port: number; healthPath: string; composeFile?: string; nodeVersion?: string; pythonVersion?: string }

export interface ApiToken { id: string; userId: string; name: string; scopes: string; lastUsedAt: string; expiresAt: string; createdAt: string; prefix?: string }

export interface RunnerPool { id: string; provider: string; name: string; url: string; token?: string; labels: string; minIdle: number; maxRunners: number; dockerAccess: boolean; memoryMb: number; cpus: number; webhookSecret?: string; enabled: boolean; createdAt: string; runners: { name: string; state: string; busy: boolean; started: string }[]; idle: number; busy: number; error?: string }
export interface RunnerJob { id: number; poolId: string; externalId: string; name: string; repo: string; runner: string; status: string; conclusion: string; url: string; queuedAt: string; startedAt: string; finishedAt: string }

export interface SecCheck { id: string; title: string; detail: string; weight: number; status: "pass" | "fail" | "warn" | "unknown"; fix?: string; fixNote?: string }
export interface SecReport { score: number; max: number; checks: SecCheck[]; linux: boolean; computedAt: string }
export interface FirewallRule { port: string; proto: string; from: string; comment: string }
export interface SSHSettings { port: number; permitRootLogin: boolean; passwordAuth: boolean; pubkeyAuth: boolean; maxAuthTries: number; allowAgentForwarding: boolean; x11Forwarding: boolean; clientAliveCountMax: number }
export interface Scan { target: string; at: string; critical: number; high: number; medium: number; low: number; findings: { id: string; package: string; version: string; fixed: string; severity: string; title: string }[]; error?: string; truncated?: boolean }
export interface SecurityState { report: SecReport; firewall: { installed: boolean; active: boolean; rules: FirewallRule[]; dockerAware: boolean }; ssh: SSHSettings; sshHasKeys: boolean; sshRollback: boolean; banned: string[]; scans: Scan[]; clientIp: string }

export interface BackupDestination { id: string; name: string; type: "s3" | "sftp" | "local" | "rest"; config: Record<string, string>; password?: string; lastCheck: string; checkOk: boolean; createdAt: string; repo: string }
export interface BackupSource { type: "volume" | "path" | "database" | "islet"; value: string }
export interface BackupPlan { id: string; name: string; destinationId: string; sources: BackupSource[]; schedule: string; keepDaily: number; keepWeekly: number; keepMonthly: number; keepYearly: number; enabled: boolean; nextRunAt: string; lastRunAt: string; lastStatus: string; createdAt: string; described: string; running: boolean; stale: boolean }
export interface BackupRun { id: number; planId: string; trigger: string; status: string; snapshot: string; filesNew: number; filesChanged: number; bytesAdded: number; bytesTotal: number; log?: string; error: string; startedAt: string; finishedAt: string; durationMs: number }
export interface Snapshot { id: string; time: string; paths: string[]; tags: string[]; size: number }
export interface BackupOverview { destinations: BackupDestination[]; plans: BackupPlan[]; health: { plans: number; destinations: number; lastSuccess: string; nextRun: string; stale: number; failed: number; lastVerified: string }; volumes: string[]; databases?: string[] }

export class RequestError extends Error {
  status: number;
  body: ApiError;
  constructor(status: number, body: ApiError) {
    super(body.message || `request failed with ${status}`);
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let body: ApiError = { error: "http_error", message: res.statusText };
    try { body = (await res.json()) as ApiError; } catch { /* keep default */ }
    throw new RequestError(res.status, body);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
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
  setupStatus: () => request<{ needsSetup: boolean }>("/api/v1/setup"),
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
  proxyInstall: (acmeEmail: string) => post<ProxyStatus>("/api/v1/proxy/install", { acmeEmail }),
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
  installedApps: () => request<InstalledApp[]>("/api/v1/catalog/installed"),
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
  destinationVerify: (id: string) => post<{ output: string }>(`/api/v1/backups/destinations/${id}/verify`),
  snapshots: (id: string, plan?: string) => request<Snapshot[]>(`/api/v1/backups/destinations/${id}/snapshots${plan ? `?plan=${encodeURIComponent(plan)}` : ""}`),
  snapshotLs: (id: string, snap: string, path: string) => request<{ path: string; name: string; type: string; size?: number; mtime?: string }[]>(`/api/v1/backups/destinations/${id}/snapshots/${snap}/ls?path=${encodeURIComponent(path)}`),
  restore: (id: string, b: { snapshot: string; include: string; newVolume: string }) => post<{ target: string }>(`/api/v1/backups/destinations/${id}/restore`, b),
  planSave: (p: Partial<BackupPlan>) => p.id ? post<BackupPlan>(`/api/v1/backups/plans/${p.id}`, p, "PUT") : post<BackupPlan>("/api/v1/backups/plans", p),
  planDelete: (id: string) => post<void>(`/api/v1/backups/plans/${id}`, undefined, "DELETE"),
  planCancel: (id: string) => post<void>(`/api/v1/backups/plans/${id}/cancel`),
  planRuns: (id: string) => request<BackupRun[]>(`/api/v1/backups/plans/${id}/runs`),
  planRun: (id: string, run: number) => request<BackupRun>(`/api/v1/backups/plans/${id}/runs/${run}`),
  security: () => request<SecurityState>("/api/v1/security"),
  securityFix: (id: string) => post<{ output: string }>(`/api/v1/security/fix/${id}`),
  firewallAllow: (b: { port: string; proto: string; from: string; comment: string }) => post<void>("/api/v1/security/firewall/rules", b),
  firewallDelete: (b: { port: string; proto: string; from: string }) => post<void>("/api/v1/security/firewall/rules", b, "DELETE"),
  unban: (ip: string) => post<void>("/api/v1/security/unban", { ip }),
  sshApply: (cfg: SSHSettings) => post<{ message: string }>("/api/v1/security/ssh", cfg),
  sshConfirm: () => post<void>("/api/v1/security/ssh/confirm"),
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
  cronImport: (text: string, save: boolean) => post<Job[]>("/api/v1/cron/import", { text, save }),
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
  metricsHistory: (range: "1h" | "6h" | "24h" | "7d") => request<{ stepSeconds: number; points: Point[] }>(`/api/v1/metrics/history?range=${range}`),
};
