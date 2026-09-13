import { apiPath } from "@/lib/api";

/**
 * The SQL client's own API.
 *
 * It lives apart from lib/api.ts because it is a page's worth of surface that
 * only one page uses, and because the run endpoint is a stream rather than a
 * request: everything else here is ordinary JSON, and `run` is the reason this
 * file exists at all.
 *
 * Types mirror internal/sqlclient. Where a name here differs from the Go one,
 * the Go one is right.
 */

export type Engine = "postgres" | "mysql" | "mariadb";
export type Environment = "development" | "staging" | "production";

export interface Connection {
  ref: string;
  name: string;
  engine: Engine;
  host: string;
  port: number;
  database: string;
  /** Installed by Islet: nothing to configure and no password stored here. */
  managed: boolean;
  readOnly: boolean;
  environment: Environment;
  state?: string;
}

export type Class =
  | "string" | "number" | "decimal" | "bool" | "date" | "time" | "datetime"
  | "interval" | "json" | "array" | "binary" | "uuid" | "unknown";

export interface Column { name: string; type: string; class: Class; table?: string; key?: boolean }

/** One cell, already in its JSON representation. `null` is SQL NULL. */
export type Cell = unknown;

export interface ColumnInfo {
  name: string; type: string; class: Class; nullable: boolean;
  default?: string; identity?: boolean; primaryKey?: boolean; position: number; comment?: string;
}

export interface ForeignKey {
  name: string; schema: string; table: string; columns: string[];
  refSchema: string; refTable: string; refColumns: string[]; onDelete?: string; onUpdate?: string;
}

export interface Table {
  schema: string; name: string; kind: "table" | "view" | "matview" | "foreign";
  rows: number; comment?: string; columns?: ColumnInfo[];
  foreignKeys?: ForeignKey[]; referencedBy?: ForeignKey[];
}

export interface Index { name: string; columns: string[]; unique: boolean; primary: boolean; method?: string }
export interface Constraint { name: string; type: string; definition: string }
export interface TableDetail extends Table { indexes: Index[]; constraints: Constraint[] }

export interface Tree {
  engine: string;
  schemas: { name: string; tables: Table[] }[];
  fetchedAt: string;
  statStatements: boolean;
  partial?: boolean;
  note?: string;
}

export interface Match {
  kind: "schema" | "table" | "view" | "matview" | "foreign" | "column";
  schema: string; table?: string; column?: string; type?: string; label: string;
}

export type Kind = "read" | "write" | "ddl" | "transaction" | "session" | "utility" | "unknown";
export type Danger = "unfiltered" | "drop" | "truncate" | "alter" | "grant";

export interface QueryError {
  message: string; code?: string; detail?: string; hint?: string; position?: number;
}

export interface StatementResult {
  index: number;
  sql: string;
  start: number;
  end: number;
  line: number;
  kind: Kind;
  danger?: Danger[];
  columns?: Column[];
  rows?: Cell[][];
  rowCount: number;
  truncated: boolean;
  rowsAffected: number;
  durationMs: number;
  error?: QueryError;
}

export interface Summary {
  runId: string; statements: number; failed: number; durationMs: number;
  schemaChanged: boolean; inTransaction: boolean;
}

export interface HistoryEntry {
  id: number; connectionRef: string; sql: string; actor: string; kind: string;
  startedAt: string; durationMs: number; rowCount: number; truncated: boolean; error?: string;
}

export interface SavedQuery {
  id: string; name: string; description: string; sql: string;
  connectionRef: string; params: string[]; createdBy: string; createdAt: string; updatedAt: string;
}

export interface Plan {
  engine: string; statement: string; analyzed: boolean;
  json?: unknown; text?: string; durationMs: number;
}

export class SqlError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
  /** The daemon refused because the interface has not confirmed yet (8.2). */
  get needsConfirmation() { return this.code === "needs_confirmation"; }
  get readOnly() { return this.code === "read_only"; }
  get busy() { return this.code === "busy"; }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(apiPath(path), {
    ...init,
    credentials: "same-origin",
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let code = "http_error";
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: string; message?: string };
      code = body.error ?? code;
      message = body.message ?? message;
    } catch { /* keep the status text */ }
    throw new SqlError(res.status, code, message);
  }
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

function send<T>(path: string, body?: unknown, method = "POST"): Promise<T> {
  return request<T>(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

const conn = (ref: string) => `/api/v1/sql/connections/${encodeURIComponent(ref)}`;

export interface ConnectionForm {
  name: string; engine: Engine; host: string; port: number; username: string;
  password?: string | null; database: string; tls: "disable" | "require" | "verify-full";
  readOnly: boolean; environment: Environment;
}

export interface RunOptions {
  sessionId?: string;
  runId?: string;
  rowCap?: number;
  timeoutMs?: number;
  transaction?: boolean;
  continueOnError?: boolean;
  params?: Record<string, unknown>;
  confirmed?: boolean;
}

export const sql = {
  connections: () => request<Connection[]>("/api/v1/sql/connections"),
  addConnection: (f: ConnectionForm) => send<{ id: string }>("/api/v1/sql/connections", f),
  updateConnection: (id: string, f: ConnectionForm) => send<void>(`/api/v1/sql/connections/${id}`, f, "PUT"),
  deleteConnection: (id: string) => send<void>(`/api/v1/sql/connections/${id}`, undefined, "DELETE"),
  /** Read-only and environment for a connection with no row of its own. */
  mark: (ref: string, m: { readOnly: boolean; environment: Environment }) =>
    send<void>(`${conn(ref)}/mark`, m, "PUT"),
  test: (ref: string) => send<{ ok: boolean; version: string; latencyMs: number }>(`${conn(ref)}/test`),
  schema: (ref: string, refresh = false) =>
    request<Tree>(`${conn(ref)}/schema${refresh ? "?refresh=1" : ""}`),
  search: (ref: string, q: string) =>
    request<Match[]>(`${conn(ref)}/search?q=${encodeURIComponent(q)}`),
  table: (ref: string, schema: string, table: string) =>
    request<TableDetail>(`${conn(ref)}/table/${encodeURIComponent(schema)}/${encodeURIComponent(table)}`),
  cancel: (ref: string, runId: string) => send<void>(`${conn(ref)}/cancel`, { runId }),
  explain: (ref: string, body: { sql: string; sessionId?: string; analyze: boolean }) =>
    send<Plan>(`${conn(ref)}/explain`, {
      sql: body.sql, sessionId: body.sessionId ?? "", analyze: body.analyze,
    }),
  openSession: (ref: string) => send<{ id: string; ref: string; engine: string }>("/api/v1/sql/sessions", { ref }),
  closeSession: (id: string) => send<void>(`/api/v1/sql/sessions/${id}`, undefined, "DELETE"),
  history: (q: { ref?: string; q?: string; limit?: number } = {}) => {
    const p = new URLSearchParams();
    if (q.ref) p.set("ref", q.ref);
    if (q.q) p.set("q", q.q);
    p.set("limit", String(q.limit ?? 200));
    return request<HistoryEntry[]>(`/api/v1/sql/history?${p}`);
  },
  clearHistory: () => send<void>("/api/v1/sql/history", undefined, "DELETE"),
  saved: () => request<SavedQuery[]>("/api/v1/sql/saved"),
  save: (b: { name: string; description: string; sql: string; connectionRef: string; engine: string }) =>
    send<SavedQuery>("/api/v1/sql/saved", b),
  updateSaved: (id: string, b: { name: string; description: string; sql: string; connectionRef: string; engine: string }) =>
    send<void>(`/api/v1/sql/saved/${id}`, b, "PUT"),
  deleteSaved: (id: string) => send<void>(`/api/v1/sql/saved/${id}`, undefined, "DELETE"),
};

/** What a run reports as it happens. */
export interface RunHandlers {
  onRun?: (runId: string) => void;
  onStatement: (r: StatementResult) => void;
  onEnd: (s: Summary) => void;
  onError?: (message: string) => void;
}

/**
 * Run a document and read the stream.
 *
 * The response is server-sent events over a POST, which EventSource cannot do,
 * so the body is read by hand. The refusals that matter — read-only, a
 * statement that needs confirming — arrive as a status code before the stream
 * starts, which is why they can be thrown rather than delivered as an event.
 */
export async function run(
  ref: string,
  document: string,
  opt: RunOptions,
  on: RunHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const res = await fetch(apiPath(`${conn(ref)}/query`), {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    signal,
    body: JSON.stringify({
      sql: document,
      sessionId: opt.sessionId ?? "",
      runId: opt.runId ?? "",
      rowCap: opt.rowCap ?? 0,
      timeoutMs: opt.timeoutMs ?? 0,
      transaction: !!opt.transaction,
      continueOnError: !!opt.continueOnError,
      params: opt.params ?? null,
      confirmed: !!opt.confirmed,
    }),
  });

  if (!res.ok) {
    let code = "http_error";
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: string; message?: string };
      code = body.error ?? code;
      message = body.message ?? message;
    } catch { /* keep the status text */ }
    throw new SqlError(res.status, code, message);
  }
  if (!res.body) throw new SqlError(500, "stream", "the browser gave no response body to read");

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    // Events are separated by a blank line; a partial one waits for more.
    let cut: number;
    while ((cut = buffer.indexOf("\n\n")) >= 0) {
      const chunk = buffer.slice(0, cut);
      buffer = buffer.slice(cut + 2);
      let event = "message";
      let data = "";
      for (const line of chunk.split("\n")) {
        if (line.startsWith("event: ")) event = line.slice(7).trim();
        else if (line.startsWith("data: ")) data += line.slice(6);
      }
      if (!data) continue;
      const parsed = JSON.parse(data) as unknown;
      if (event === "run") on.onRun?.((parsed as { runId: string }).runId);
      else if (event === "statement") on.onStatement(parsed as StatementResult);
      else if (event === "end") on.onEnd(parsed as Summary);
      else if (event === "error") on.onError?.((parsed as { message: string }).message);
    }
  }
}
