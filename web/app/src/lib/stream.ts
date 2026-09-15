import { apiPath } from "@/lib/api";

/** Subscribe to an SSE endpoint that emits "line" events and one "end" event. */
export function streamLines(path: string, onLine: (l: string) => void, onEnd?: (msg: string) => void): () => void {
  const es = new EventSource(apiPath(path));
  es.addEventListener("line", (ev) => onLine(JSON.parse((ev as MessageEvent).data) as string));
  es.addEventListener("end", (ev) => { onEnd?.(JSON.parse((ev as MessageEvent).data) as string); es.close(); });
  // EventSource retries on its own. Closing on the first error turned one
  // dropped packet into a permanently dead log follow; only give up once the
  // browser itself has, which is what CLOSED means here.
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED) onEnd?.("connection lost");
  };
  return () => es.close();
}

/** POST an action whose response is an SSE stream of lines (Compose up, image pull). */
export async function postStream(path: string, onLine: (l: string) => void, body?: unknown, signal?: AbortSignal): Promise<void> {
  const res = await fetch(apiPath(path), { method: "POST", headers: { "Content-Type": "application/json", Accept: "text/event-stream" }, credentials: "same-origin", body: body === undefined ? undefined : JSON.stringify(body), signal });
  if (!res.ok || !res.body) {
    let msg = res.statusText;
    try { msg = ((await res.json()) as { message: string }).message; } catch { /* ignore */ }
    throw new Error(msg);
  }
  const reader = res.body.getReader();
  signal?.addEventListener("abort", () => { void reader.cancel(); });
  const dec = new TextDecoder();
  let buf = "";
  let failed: string | null = null;
  // A stream that stops without its end event was cut off, not finished. It
  // used to resolve as success, so a deploy killed by a daemon restart was
  // reported as done.
  let ended = false;
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const chunk = buf.slice(0, i); buf = buf.slice(i + 2);
      const ev = /^event: (\w+)/m.exec(chunk)?.[1];
      const data = /^data: (.*)$/m.exec(chunk)?.[1];
      if (!data) continue;
      const text = JSON.parse(data) as string;
      if (ev === "end") { ended = true; if (text.startsWith("error")) failed = text; }
      else onLine(text);
    }
  }
  if (failed) throw new Error(failed);
  if (!ended && !signal?.aborted) throw new Error("the connection closed before the command finished");
}

/**
 * POST an action whose response is newline-delimited JSON, one object per line.
 *
 * Used where a reply takes minutes to produce and the person should see it
 * arrive: a proxy in front of the panel gives the origin a fixed window to
 * respond — Cloudflare's is 100 seconds — so a request that answers only at the
 * end fails as a 524 no matter how well it went.
 */
export async function postNDJSON<T>(path: string, onEvent: (e: T) => void, body?: unknown, signal?: AbortSignal): Promise<void> {
  const res = await fetch(apiPath(path), { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/x-ndjson, application/json" }, credentials: "same-origin", body: body === undefined ? undefined : JSON.stringify(body), signal });
  return readNDJSON(res, onEvent, signal);
}

/**
 * Follow an endpoint that streams newline-delimited JSON.
 *
 * Used to pick a run back up: the work belongs to the server, so a phone that
 * locked its screen halfway through reattaches here rather than starting again.
 */
export async function getNDJSON<T>(path: string, onEvent: (e: T) => void, signal?: AbortSignal): Promise<void> {
  const res = await fetch(apiPath(path), { headers: { Accept: "application/x-ndjson" }, credentials: "same-origin", signal });
  return readNDJSON(res, onEvent, signal);
}

async function readNDJSON<T>(res: Response, onEvent: (e: T) => void, signal?: AbortSignal): Promise<void> {
  if (!res.ok || !res.body) {
    let msg = res.statusText;
    try { msg = ((await res.json()) as { message: string }).message; } catch { /* ignore */ }
    throw new Error(msg);
  }
  const reader = res.body.getReader();
  signal?.addEventListener("abort", () => { void reader.cancel(); });
  const dec = new TextDecoder();
  let buf = "";
  const take = (line: string) => {
    const s = line.trim();
    if (s) onEvent(JSON.parse(s) as T);
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf("\n")) >= 0) {
      take(buf.slice(0, i));
      buf = buf.slice(i + 1);
    }
  }
  // A last line without its newline is still a line.
  take(buf + dec.decode());
}
