import { useOutletContext } from "react-router-dom";
import type { Health } from "@/lib/api";

function fmtUptime(s: number) {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m ${s % 60}s`;
}

export default function Overview() {
  const { health } = useOutletContext<{ health: Health | null }>();
  return (
    <div className="mx-auto max-w-5xl">
      <h1 className="text-xl font-semibold tracking-[-0.02em]">Overview</h1>
      <p className="mt-1 text-ink-muted">What this server is doing right now.</p>

      <div className="mt-6 grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Daemon" value={health ? "Running" : "Unreachable"} tone={health ? "ok" : "bad"} />
        <Stat label="Version" value={health?.version ?? "–"} mono />
        <Stat label="Uptime" value={health ? fmtUptime(health.uptimeSeconds) : "–"} />
        <Stat label="Server" value={health ? health.serverId.slice(0, 8) : "–"} mono />
      </div>

      <section className="mt-8 rounded-lg border border-border bg-surface p-5">
        <h2 className="font-semibold">Metrics arrive next</h2>
        <p className="mt-1 max-w-prose text-ink-muted">
          Live CPU, memory, swap, disk and network, a 7-day history, top processes and listening ports are the next v0.1 items.
          This page will fill in as they land.
        </p>
      </section>
    </div>
  );
}

function Stat({ label, value, mono, tone }: { label: string; value: string; mono?: boolean; tone?: "ok" | "bad" }) {
  return (
    <div className="rounded-md border border-border bg-surface px-3.5 py-3">
      <div className="text-xs text-ink-muted">{label}</div>
      <div className={`mt-0.5 text-lg font-semibold tracking-[-0.02em] tabular-nums ${mono ? "font-mono text-base" : ""} ${tone === "ok" ? "text-success" : tone === "bad" ? "text-danger" : ""}`}>
        {value}
      </div>
    </div>
  );
}
