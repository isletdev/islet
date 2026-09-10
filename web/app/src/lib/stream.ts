/** Subscribe to an SSE endpoint that emits "line" events and one "end" event. */
export function streamLines(path: string, onLine: (l: string) => void, onEnd?: (msg: string) => void): () => void {
  const es = new EventSource(path);
  es.addEventListener("line", (ev) => onLine(JSON.parse((ev as MessageEvent).data) as string));
  es.addEventListener("end", (ev) => { onEnd?.(JSON.parse((ev as MessageEvent).data) as string); es.close(); });
  es.onerror = () => { onEnd?.("connection lost"); es.close(); };
  return () => es.close();
}

/** POST an action whose response is an SSE stream of lines (Compose up, image pull). */
export async function postStream(path: string, onLine: (l: string) => void): Promise<void> {
  const res = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json", Accept: "text/event-stream" }, credentials: "same-origin" });
  if (!res.ok || !res.body) {
    let msg = res.statusText;
    try { msg = ((await res.json()) as { message: string }).message; } catch { /* ignore */ }
    throw new Error(msg);
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  let failed: string | null = null;
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
      if (ev === "end") { if (text.startsWith("error")) failed = text; }
      else onLine(text);
    }
  }
  if (failed) throw new Error(failed);
}
