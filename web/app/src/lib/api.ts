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
};
