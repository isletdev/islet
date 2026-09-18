import { useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api, type LogSource } from "@/lib/api";
import { streamLines } from "@/lib/stream";
import { Alert, Button, Input, Select } from "@/components/ui";

const MAX_LINES = 5000;

export default function Logs() {
  const [params, setParams] = useSearchParams();
  const [sources, setSources] = useState<LogSource[]>([]);
  const [lines, setLines] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [follow, setFollow] = useState(true);
  const [tail, setTail] = useState(200);
  const [state, setState] = useState<"idle" | "streaming" | "ended">("idle");
  const [error, setError] = useState<string | null>(null);
  const [wrap, setWrap] = useState(false);
  const box = useRef<HTMLPreElement>(null);
  const stick = useRef(true);
  const source = params.get("source") ?? "";
  const [gen, setGen] = useState(0);

  useEffect(() => { void api.logSources().then((s) => { setSources(s); if (!source && s.length) setParams({ source: s[0].id }, { replace: true }); }).catch((e) => setError(String(e))); }, [source, setParams]);

  useEffect(() => {
    if (!source) return;
    setLines([]); setState("streaming"); setError(null);
    const stop = streamLines(`/api/v1/logs/stream?source=${encodeURIComponent(source)}&tail=${tail}&follow=${follow ? 1 : 0}`,
      (l) => setLines((p) => (p.length >= MAX_LINES ? [...p.slice(-MAX_LINES + 1), l] : [...p, l])),
      (end) => { setState("ended"); if (!end.ok) setError(end.error === "connection_lost" ? "The source could not be opened or the connection dropped. Pick another source or reload." : (end.message ?? "The stream ended before the source did.")); });
    return stop;
  }, [source, tail, follow, gen]);

  useEffect(() => { if (stick.current && box.current) box.current.scrollTop = box.current.scrollHeight; }, [lines]);

  const shown = useMemo(() => {
    if (!filter) return lines;
    const f = filter.toLowerCase();
    return lines.filter((l) => l.toLowerCase().includes(f));
  }, [lines, filter]);
  const groups = useMemo(() => { const g: Record<string, LogSource[]> = {}; for (const s of sources) (g[s.group] ??= []).push(s); return g; }, [sources]);

  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col gap-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="sr-only">Logs</h1>
          <p className="mt-1 text-ink-muted">System journal, log files and every container in one place.</p>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Select value={source} onChange={(e) => setParams({ source: e.target.value })} className="sm:w-auto sm:min-w-56">
          {Object.entries(groups).map(([g, list]) => <optgroup key={g} label={g}>{list.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}</optgroup>)}
          {sources.length === 0 && <option value="">No sources available</option>}
        </Select>
        <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter lines…" className="w-full sm:w-56" />
        <Select value={tail} onChange={(e) => setTail(+e.target.value)} className="w-auto">{[100, 200, 500, 1000, 5000].map((n) => <option key={n} value={n}>last {n}</option>)}</Select>
        <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />Follow</label>
        <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={wrap} onChange={(e) => setWrap(e.target.checked)} />Wrap</label>
        <Button variant="secondary" className="h-9 text-xs" onClick={() => setGen((g) => g + 1)}>Reload</Button>
        <Button variant="secondary" className="h-9 text-xs" onClick={() => { const blob = new Blob([shown.join("\n")], { type: "text/plain" }); const a = document.createElement("a"); a.href = URL.createObjectURL(blob); a.download = `${source.replace(/[^a-z0-9]+/gi, "-")}.log`; a.click(); }}>Download</Button>
        <span className="ml-auto text-xs text-ink-muted">{shown.length}{filter && ` of ${lines.length}`} lines · {state === "streaming" ? (follow ? "live" : "loaded") : "stream ended"}</span>
      </div>
      {error && <Alert>{error}</Alert>}
      <pre ref={box} onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40; }} className={`min-h-0 flex-1 overflow-auto rounded-lg border border-border bg-[#0A0A0A] p-3 font-mono text-xs leading-5 text-[#FAFAFA] ${wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre"}`}>
        {shown.length === 0 ? <span className="text-ink-faint">{state === "streaming" ? "Waiting for lines…" : "Nothing here."}</span> : shown.map((l, i) => <Line key={i} text={l} highlight={filter} />)}
      </pre>
    </div>
  );
}

function Line({ text, highlight }: { text: string; highlight: string }) {
  const lower = text.toLowerCase();
  const tone = /\b(error|fatal|panic|crit)/i.test(text) ? "text-danger" : /\bwarn/i.test(text) ? "text-warning" : "";
  if (!highlight) return <div className={tone}>{text}</div>;
  const i = lower.indexOf(highlight.toLowerCase());
  if (i < 0) return <div className={tone}>{text}</div>;
  return <div className={tone}>{text.slice(0, i)}<mark className="bg-accent-soft text-ink">{text.slice(i, i + highlight.length)}</mark>{text.slice(i + highlight.length)}</div>;
}
