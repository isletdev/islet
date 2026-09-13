import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type Attention as AttentionData } from "@/lib/api";
import { pollInterval } from "@/lib/poll";

/** What needs attention across the server: security score, backups, checks, deploys, jobs, criticals. */
export default function Attention() {
  const [a, setA] = useState<AttentionData | null>(null);
  useEffect(() => { const load = () => api.attention().then(setA).catch(() => {}); load(); const stop = pollInterval(load, 60000); return stop; }, []);
  if (!a) return null;
  const b = a.backups;
  const issues: { text: string; to: string; tone: "danger" | "warning" }[] = [];
  for (const n of a.checksDown) issues.push({ text: `${n} is down`, to: "/uptime", tone: "danger" });
  for (const n of a.deploysFailed) issues.push({ text: `Last deploy of ${n} failed`, to: "/apps", tone: "danger" });
  for (const n of a.jobsFailed) issues.push({ text: `Job ${n} failed or is overdue`, to: "/cron", tone: "warning" });
  if (b && b.plans === 0) issues.push({ text: "No backup plan yet", to: "/backups", tone: "warning" });
  if (b && b.stale > 0) issues.push({ text: `${b.stale} backup plan${b.stale === 1 ? "" : "s"} stale`, to: "/backups", tone: "danger" });
  if (b && b.failed > 0) issues.push({ text: `${b.failed} backup plan${b.failed === 1 ? "" : "s"} failed`, to: "/backups", tone: "danger" });
  for (const c of a.criticals.slice(0, 3)) issues.push({ text: c.title, to: c.link || "/notifications", tone: "danger" });
  const tone = a.securityScore >= 90 ? "text-success" : a.securityScore >= 60 ? "text-warning" : "text-danger";
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-[180px_minmax(0,1fr)]">
      <Link to="/security" className="rounded-lg border border-border bg-surface p-3 transition-colors hover:bg-surface-2">
        <div className="text-xs text-ink-muted">Security Score</div>
        <div className={`mt-1 font-mono text-3xl font-semibold tabular-nums ${tone}`}>{a.securityScore}</div>
        <div className="text-xs text-ink-muted">{a.securityFailing === 0 ? "all checks pass" : `${a.securityFailing} to fix`}</div>
      </Link>
      <div className="rounded-lg border border-border bg-surface p-3">
        <div className="flex items-center justify-between text-xs text-ink-muted"><span>Needs attention</span>{b && b.lastSuccess && <span>last backup {new Date(b.lastSuccess).toLocaleString()}</span>}</div>
        {issues.length === 0 ? <div className="mt-1 text-sm text-success">Nothing. Checks up, deploys green, jobs on time{b && b.plans > 0 ? ", backups fresh" : ""}.</div> : (
          <ul className="mt-1 flex flex-wrap gap-2">{issues.map((i, k) => <li key={k}><Link to={i.to} className={`rounded-sm border px-2 py-0.5 text-xs ${i.tone === "danger" ? "border-danger/40 bg-danger-soft text-danger" : "border-warning/40 bg-warning-soft text-warning"}`}>{i.text}</Link></li>)}</ul>
        )}
      </div>
    </div>
  );
}
