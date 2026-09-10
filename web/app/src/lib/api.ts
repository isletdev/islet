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
