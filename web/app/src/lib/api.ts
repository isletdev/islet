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
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let body: ApiError = { error: "http_error", message: res.statusText };
    try { body = (await res.json()) as ApiError; } catch { /* keep default */ }
    throw new RequestError(res.status, body);
  }
  return (await res.json()) as T;
}

export const api = {
  health: () => request<Health>("/api/v1/health"),
};
