import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api, type HostInfo, type Point, type Process, type Port, type Sample } from "@/lib/api";
import { bytes, duration, pct, rate } from "@/lib/format";
import Sparkline, { type SparkPoint } from "@/components/Sparkline";
import { Card } from "@/components/ui";
import DiskDoctor from "@/components/DiskDoctor";
import { useAuth } from "@/lib/auth";

type Range = "1h" | "6h" | "24h" | "7d";

export default function Overview() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [latest, setLatest] = useState<Sample | null>(null);
  const [recent, setRecent] = useState<Sample[]>([]);
  const [range, setRange] = useState<Range>("1h");
  const [history, setHistory] = useState<Point[]>([]);
  const [host, setHost] = useState<HostInfo | null>(null);
  const [procs, setProcs] = useState<Process[]>([]);
  const [ports, setPorts] = useState<Port[]>([]);

  // Live stream with a polling fallback.
  useEffect(() => {
    let es: EventSource | null = null;
    let alive = true;
    api.metricsLatest().then((r) => { if (!alive) return; setLatest(r.latest); setRecent(r.recent ?? []); }).catch(() => {});
    try {
      es = new EventSource("/api/v1/metrics/live");
      es.addEventListener("sample", (ev) => {
        const s = JSON.parse((ev as MessageEvent).data) as Sample;
        setLatest(s);
        setRecent((r) => [...r.slice(-149), s]);
      });
    } catch { /* EventSource unavailable */ }
    return () => { alive = false; es?.close(); };
  }, []);

  useEffect(() => {
    let alive = true;
    const load = () => api.metricsHistory(range).then((r) => alive && setHistory(r.points)).catch(() => {});
    load();
    const id = setInterval(load, 30000);
    return () => { alive = false; clearInterval(id); };
  }, [range]);

  useEffect(() => {
    api.system().then(setHost).catch(() => {});
    const load = () => {
      api.processes(12).then(setProcs).catch(() => {});
      api.ports().then(setPorts).catch(() => {});
    };
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, []);

  // The 1h view uses stored 30 s buckets plus the live ring for the freshest minutes.
  const series = useMemo(() => {
    const src: { ts: number; cpuPct: number; memUsed: number; memTotal: number; diskUsed: number; diskTotal: number; netRx: number; netTx: number }[] =
      history.map((p) => ({ ...p }));
    if (range === "1h") {
      const lastTs = src.length ? src[src.length - 1].ts : 0;
      for (const s of recent) {
        const ts = Math.floor(new Date(s.ts).getTime() / 1000);
        if (ts > lastTs) src.push({ ts, cpuPct: s.cpuPct, memUsed: s.memUsed, memTotal: s.memTotal, diskUsed: s.diskUsed, diskTotal: s.diskTotal, netRx: s.netRx, netTx: s.netTx });
      }
    }
    const pick = (f: (p: (typeof src)[number]) => number): SparkPoint[] => src.map((p) => ({ ts: p.ts, value: f(p) }));
    return {
      cpu: pick((p) => p.cpuPct),
      mem: pick((p) => (p.memTotal ? (p.memUsed / p.memTotal) * 100 : 0)),
      disk: pick((p) => (p.diskTotal ? (p.diskUsed / p.diskTotal) * 100 : 0)),
      net: pick((p) => p.netRx + p.netTx),
    };
  }, [history, recent, range]);

  const memPct = latest && latest.memTotal ? (latest.memUsed / latest.memTotal) * 100 : 0;
  const diskPct = latest && latest.diskTotal ? (latest.diskUsed / latest.diskTotal) * 100 : 0;

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Overview</h1>
          <p className="mt-1 text-ink-muted">{host ? `${host.platform} ${host.platformVersion} · ${host.arch} · ${host.cpuCount} CPUs · up ${duration(host.uptimeSeconds)}` : "What this server is doing right now."}</p>
        </div>
        <div className="flex rounded-md border border-border-strong p-0.5 text-xs font-medium" role="group" aria-label="Time range">
          {(["1h", "6h", "24h", "7d"] as Range[]).map((r) => (
            <button key={r} type="button" onClick={() => setRange(r)} className={`rounded-sm px-2.5 py-1 ${range === r ? "bg-ink text-on-ink" : "text-ink-muted hover:text-ink"}`} aria-pressed={range === r}>{r}</button>
          ))}
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <Tile label="CPU" value={latest ? pct(latest.cpuPct) : "–"} sub={latest ? `load ${latest.load1.toFixed(2)} / ${latest.load5.toFixed(2)} / ${latest.load15.toFixed(2)}` : ""} warn={!!latest && latest.cpuPct >= 90}>
          <Sparkline points={series.cpu} max={100} format={pct} />
        </Tile>
        <Tile label="Memory" value={latest ? pct(memPct) : "–"} sub={latest ? `${bytes(latest.memUsed)} of ${bytes(latest.memTotal)}${latest.swapTotal ? ` · swap ${bytes(latest.swapUsed)}` : ""}` : ""} warn={memPct >= 90}>
          <Sparkline points={series.mem} max={100} format={pct} />
        </Tile>
        <Tile label="Disk" value={latest ? pct(diskPct) : "–"} sub={latest ? `${bytes(latest.diskUsed)} of ${bytes(latest.diskTotal)}` : ""} warn={diskPct >= 85}>
          <Sparkline points={series.disk} max={100} format={pct} />
        </Tile>
        <Tile label="Network" value={latest ? rate(latest.netRx + latest.netTx) : "–"} sub={latest ? `↓ ${rate(latest.netRx)} · ↑ ${rate(latest.netTx)}` : ""}>
          <Sparkline points={series.net} format={rate} />
        </Tile>
      </div>

      <div className="grid gap-6 lg:grid-cols-[1.4fr_1fr]">
        <Card title="Top processes" description="By CPU, then memory. Refreshes every 10 seconds.">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-ink-muted">
              <tr><th className="pb-2 font-medium">Process</th><th className="pb-2 font-medium">User</th><th className="pb-2 text-right font-medium">CPU</th><th className="pb-2 text-right font-medium">Memory</th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {procs.map((p) => (
                <tr key={p.pid}>
                  <td className="py-1.5"><span className="font-medium">{p.name}</span> <span className="font-mono text-xs text-ink-faint">{p.pid}</span></td>
                  <td className="py-1.5 text-ink-muted">{p.user}</td>
                  <td className="py-1.5 text-right font-mono tabular-nums">{pct(p.cpuPct)}</td>
                  <td className="py-1.5 text-right font-mono tabular-nums">{bytes(p.memRss, 0)}</td>
                </tr>
              ))}
              {procs.length === 0 && <tr><td colSpan={4} className="py-2 text-ink-muted">Reading processes…</td></tr>}
            </tbody>
          </table>
        </Card>
        <Card title="Listening ports" description="Sockets accepting connections on this server.">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-ink-muted">
              <tr><th className="pb-2 font-medium">Port</th><th className="pb-2 font-medium">Address</th><th className="pb-2 font-medium">Process</th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {ports.map((p) => (
                <tr key={`${p.proto}-${p.address}-${p.port}`}>
                  <td className="py-1.5 font-mono tabular-nums">{p.port}<span className="ml-1 text-xs text-ink-faint">{p.proto}</span></td>
                  <td className="py-1.5 font-mono text-xs text-ink-muted">{p.address || "*"}</td>
                  <td className="py-1.5">{p.process || <span className="text-ink-faint">pid {p.pid || "?"}</span>}</td>
                </tr>
              ))}
              {ports.length === 0 && <tr><td colSpan={3} className="py-2 text-ink-muted">Reading sockets…</td></tr>}
            </tbody>
          </table>
        </Card>
      </div>

      <DiskDoctor isAdmin={isAdmin} />

      {host && (
        <Card title="Server">
          <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm md:grid-cols-4">
            <Item k="Hostname" v={host.hostname} />
            <Item k="OS" v={`${host.platform} ${host.platformVersion}`} />
            <Item k="Kernel" v={host.kernel} mono />
            <Item k="Architecture" v={host.arch} mono />
            <Item k="CPU" v={`${host.cpuCount} × ${host.cpuModel || "unknown"}`} />
            <Item k="Memory" v={bytes(host.memTotal)} />
            <Item k="Disk" v={bytes(host.diskTotal)} />
            <Item k="Timezone" v={host.timezone} />
          </dl>
        </Card>
      )}
    </div>
  );
}

function Tile({ label, value, sub, warn, children }: { label: string; value: string; sub?: string; warn?: boolean; children: ReactNode }) {
  return (
    <div className="rounded-lg border border-border bg-surface p-4">
      <div className="flex items-baseline justify-between">
        <span className="text-xs font-medium text-ink-muted">{label}</span>
        {warn && <span className="rounded-sm bg-warning-soft px-1.5 py-0.5 text-[11px] font-medium text-warning">High</span>}
      </div>
      <div className="mt-1 text-2xl font-semibold tracking-[-0.02em] tabular-nums">{value}</div>
      <div className="mt-0.5 truncate text-xs text-ink-muted">{sub}</div>
      <div className="mt-3 text-ink">{children}</div>
    </div>
  );
}

function Item({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-xs text-ink-muted">{k}</dt>
      <dd className={`truncate ${mono ? "font-mono text-xs" : ""}`}>{v || "–"}</dd>
    </div>
  );
}
