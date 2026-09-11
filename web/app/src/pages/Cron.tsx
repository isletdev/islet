import { lazy, Suspense, useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api, RequestError, type Job, type JobRun, type JobTemplate } from "@/lib/api";
import { postStream } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";

const CodeEditor = lazy(() => import("@/components/CodeEditor"));

const TYPES: Record<string, { label: string; help: string }> = {
  command: { label: "Command", help: "One shell line, run with /bin/sh." },
  script: { label: "Script", help: "A script stored by Islet with the right permissions. Versions are kept." },
  file: { label: "Existing file", help: "Runs an executable that already exists on the server." },
  container: { label: "Container exec", help: "Runs a command inside a running container." },
  image: { label: "One-off container", help: "Starts a throwaway container from an image and removes it afterwards." },
  http: { label: "HTTP request", help: "Calls a URL. A 4xx or 5xx response counts as a failure." },
  chain: { label: "Chain", help: "Runs other jobs in order and stops at the first failure." },
  heartbeat: { label: "Heartbeat", help: "For jobs that run elsewhere. They ping a URL; Islet alerts when the ping stops." },
};
const PRESETS: [string, string][] = [["Every minute", "* * * * *"], ["Every 5 minutes", "*/5 * * * *"], ["Every 15 minutes", "*/15 * * * *"], ["Hourly", "0 * * * *"], ["Daily at 03:00", "0 3 * * *"], ["Weekdays at 08:00", "0 8 * * 1-5"], ["Weekly, Sunday 04:00", "0 4 * * 0"], ["Monthly, 1st at 04:00", "0 4 1 * *"]];
const SELECT = "h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm";

const blank = (): Partial<Job> => ({ id: "", name: "", type: "command", schedule: "0 3 * * *", timezone: "", command: "", script: "", container: "", httpMethod: "GET", workDir: "", runAs: "", timeoutSec: 3600, overlap: "skip", retries: 0, nice: 0, jitterSec: 0, graceSec: 300, notifyOn: "failure", enabled: true });

function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function dur(ms: number) { return ms < 1000 ? `${ms} ms` : ms < 60000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms / 60000)} min`; }

export default function Cron() {
  const { state } = useAuth();
  const role = state.status === "authed" ? state.me.user.role : "viewer";
  const canEdit = role === "admin";
  const [params, setParams] = useSearchParams();
  const [jobs, setJobs] = useState<Job[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<Job> | null>(null);
  const [importing, setImporting] = useState(false);
  const selected = params.get("job");

  const load = useCallback(() => api.jobs().then((j) => { setJobs(j); setErr(null); }).catch((e) => setErr(e instanceof RequestError ? e.message : String(e))), []);
  useEffect(() => { void load(); const id = setInterval(() => void load(), 10000); return () => clearInterval(id); }, [load]);

  const toggle = async (j: Job) => { await api.jobSave({ ...j, enabled: !j.enabled }); await load(); };
  const remove = async (j: Job) => { if (!confirm(`Delete job "${j.name}" and its history?`)) return; await api.jobDelete(j.id); if (selected === j.id) setParams({}); await load(); };

  const sel = jobs.find((j) => j.id === selected);
  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Cron</h1>
          <p className="mt-1 text-ink-muted">Scheduled commands, scripts and checks with live output and history.</p>
        </div>
        {canEdit && <div className="flex gap-2"><Button variant="secondary" className="h-8 text-xs" onClick={() => setImporting(true)}>Import crontab</Button><Button className="h-8 text-xs" onClick={() => setEditing(blank())}>New job</Button></div>}
      </div>
      {err && <Alert>{err}</Alert>}

      {editing && <JobEditor initial={editing} jobs={jobs} onClose={() => setEditing(null)} onSaved={async (j) => { setEditing(null); await load(); setParams({ job: j.id }); }} />}
      {importing && <ImportPanel onClose={() => setImporting(false)} onDone={async () => { setImporting(false); await load(); }} />}

      <div className="overflow-x-auto rounded-lg border border-border bg-surface">
        <table className="w-full min-w-[720px] text-sm">
          <thead className="text-left text-xs text-ink-muted"><tr className="border-b border-border">
            <th className="px-4 py-2 font-medium">Job</th><th className="px-4 py-2 font-medium">Schedule</th><th className="px-4 py-2 font-medium">Next run</th><th className="px-4 py-2 font-medium">Last run</th><th className="px-4 py-2" />
          </tr></thead>
          <tbody className="divide-y divide-border">
            {jobs.map((j) => (
              <tr key={j.id} className={`${selected === j.id ? "bg-surface-2" : "hover:bg-surface-2"} cursor-pointer`} onClick={() => setParams({ job: j.id })}>
                <td className="px-4 py-2.5"><div className="flex items-center gap-2"><span className={`h-2 w-2 rounded-full ${!j.enabled ? "bg-ink-faint" : j.overdue ? "bg-danger" : j.running ? "bg-accent animate-pulse" : "bg-success"}`} /><span className="font-medium">{j.name}</span><span className="text-xs text-ink-muted">{TYPES[j.type]?.label}</span></div></td>
                <td className="px-4 py-2.5"><div className="font-mono text-xs">{j.schedule || "manual"}</div><div className="text-xs text-ink-muted">{j.described}</div></td>
                <td className="px-4 py-2.5 text-xs text-ink-muted">{j.type === "heartbeat" ? (j.lastPingAt ? `pinged ${fmt(j.lastPingAt)}` : "waiting for first ping") : j.enabled ? fmt(j.nextRun) : "paused"}</td>
                <td className="px-4 py-2.5 text-xs">{j.lastRun ? <RunBadge r={j.lastRun} /> : <span className="text-ink-muted">never</span>}</td>
                <td className="px-4 py-2.5 text-right text-xs whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                  {canEdit && <><button type="button" className="text-ink-muted hover:text-ink" onClick={() => void toggle(j)}>{j.enabled ? "Pause" : "Resume"}</button><button type="button" className="ml-3 text-ink-muted hover:text-ink" onClick={() => setEditing({ ...j })}>Edit</button><button type="button" className="ml-3 text-danger hover:underline" onClick={() => void remove(j)}>Delete</button></>}
                </td>
              </tr>
            ))}
            {jobs.length === 0 && <tr><td colSpan={5} className="px-4 py-8 text-center text-ink-muted">No jobs yet. Create one, or import your existing crontab.</td></tr>}
          </tbody>
        </table>
      </div>

      {sel && <JobDetail job={sel} canRun={role !== "viewer"} canEdit={canEdit} onChanged={load} />}
    </div>
  );
}

function RunBadge({ r }: { r: JobRun }) {
  const tone = r.status === "success" ? "text-success" : r.status === "running" ? "text-accent" : r.status === "skipped" ? "text-ink-muted" : "text-danger";
  return <span><span className={`font-medium ${tone}`}>{r.status}</span> <span className="text-ink-muted">{fmt(r.startedAt)}{r.durationMs > 0 && ` · ${dur(r.durationMs)}`}</span></span>;
}

function JobDetail({ job, canRun, canEdit, onChanged }: { job: Job; canRun: boolean; canEdit: boolean; onChanged: () => Promise<void> }) {
  const [runs, setRuns] = useState<JobRun[]>([]);
  const [open, setOpen] = useState<JobRun | null>(null);
  const [live, setLive] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [exp, setExp] = useState<{ crontab: string; scriptPath: string } | null>(null);
  const outRef = useRef<HTMLPreElement>(null);
  const loadRuns = useCallback(() => api.jobRuns(job.id).then(setRuns).catch(() => {}), [job.id]);
  useEffect(() => { void loadRuns(); setOpen(null); setLive(null); setExp(null); }, [loadRuns]);
  useEffect(() => { outRef.current?.scrollTo(0, outRef.current.scrollHeight); }, [live]);

  const runNow = async () => {
    setBusy(true); setLive([]); setOpen(null);
    try { await postStream(`/api/v1/cron/jobs/${job.id}/run`, (l) => setLive((p) => [...(p ?? []), l])); setLive((p) => [...(p ?? []), "[islet] finished: success"]); }
    catch (e) { setLive((p) => [...(p ?? []), `[islet] ${e instanceof Error ? e.message : String(e)}`]); }
    finally { setBusy(false); await loadRuns(); await onChanged(); }
  };
  const stop = async () => { try { await api.jobKill(job.id); } catch { /* not running */ } };
  const show = async (r: JobRun) => { setLive(null); setOpen(await api.jobRun(job.id, r.id)); };
  const pingUrl = `${location.origin}/api/v1/ping/${job.command}`;

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
      <Card title={job.name} description={TYPES[job.type]?.help}>
        <div className="flex flex-wrap items-center gap-2">
          {job.type === "heartbeat" ? (
            <div className="w-full text-sm">
              <p className="text-ink-muted">Have the external job call this URL when it finishes. Islet expects a ping for every scheduled slot, with {job.graceSec}s of grace.</p>
              <pre className="mt-2 overflow-x-auto rounded-md border border-border bg-bg p-3 font-mono text-xs">curl -fsS {pingUrl}</pre>
              {job.overdue && <Alert>No ping arrived by the expected time. The last one was {job.lastPingAt ? fmt(job.lastPingAt) : "never"}.</Alert>}
            </div>
          ) : (
            <>
              {canRun && <Button className="h-8 text-xs" onClick={() => void runNow()} disabled={busy || job.running}>{busy || job.running ? "Running…" : "Run now"}</Button>}
              {canRun && (busy || job.running) && <Button variant="danger" className="h-8 text-xs" onClick={() => void stop()}>Stop</Button>}
              {canEdit && <Button variant="secondary" className="h-8 text-xs" onClick={async () => setExp(await api.jobExport(job.id))}>Export as crontab</Button>}
            </>
          )}
        </div>
        {exp && <pre className="mt-3 overflow-x-auto rounded-md border border-border bg-bg p-3 font-mono text-xs">{exp.crontab}{job.type === "script" && `\n# script file: ${exp.scriptPath}`}</pre>}
        {(live || open) && (
          <div className="mt-3">
            <div className="mb-1 flex items-center justify-between text-xs text-ink-muted"><span>{open ? <>Run #{open.id} · {open.trigger} · <RunBadge r={open} /> · exit {open.exitCode}</> : "Live output"}</span><button type="button" onClick={() => { setLive(null); setOpen(null); }} className="hover:text-ink">Close</button></div>
            <pre ref={outRef} className="max-h-96 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{open ? open.output || "(no output)" : (live ?? []).join("\n") || "waiting for output…"}</pre>
          </div>
        )}
      </Card>
      <Card title="History" description="Newest first. Click a run to see its output.">
        <ul className="divide-y divide-border text-xs">
          {runs.map((r) => <li key={r.id}><button type="button" onClick={() => void show(r)} className={`w-full py-1.5 text-left hover:text-ink ${open?.id === r.id ? "text-ink" : ""}`}><RunBadge r={r} /><span className="ml-1 text-ink-faint">{r.trigger}{r.attempt > 1 && ` #${r.attempt}`}</span></button></li>)}
          {runs.length === 0 && <li className="py-2 text-ink-muted">No runs yet. Press Run now to try it; every run keeps its output here.</li>}
        </ul>
        {runs.filter((r) => r.durationMs > 0).length > 1 && <DurationBars runs={runs} />}
      </Card>
    </div>
  );
}

function DurationBars({ runs }: { runs: JobRun[] }) {
  const pts = runs.filter((r) => r.durationMs > 0).slice(0, 30).reverse();
  const max = Math.max(...pts.map((r) => r.durationMs));
  return (
    <div className="mt-3 border-t border-border pt-3">
      <div className="mb-1 text-xs text-ink-muted">Duration, last {pts.length} runs · max {dur(max)}</div>
      <div className="flex h-12 items-end gap-0.5">{pts.map((r) => <div key={r.id} title={`${dur(r.durationMs)} · ${r.status}`} className={`flex-1 rounded-sm ${r.status === "success" ? "bg-ink/60" : "bg-danger"}`} style={{ height: `${Math.max(6, (r.durationMs / max) * 100)}%` }} />)}</div>
    </div>
  );
}

function JobEditor({ initial, jobs, onClose, onSaved }: { initial: Partial<Job>; jobs: Job[]; onClose: () => void; onSaved: (j: Job) => Promise<void> }) {
  const [j, setJ] = useState<Partial<Job>>(initial);
  const [preview, setPreview] = useState<{ described: string; next: string[]; error?: string } | null>(null);
  const [templates, setTemplates] = useState<JobTemplate[]>([]);
  const [versions, setVersions] = useState<{ id: number; createdAt: string; actor: string }[]>([]);
  const [lint, setLint] = useState<string | null>(null);
  const [containers, setContainers] = useState<string[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  const [advanced, setAdvanced] = useState(false);
  const [builder, setBuilder] = useState(false);
  const [busy, setBusy] = useState(false);
  const set = (p: Partial<Job>) => setJ((c) => ({ ...c, ...p }));

  useEffect(() => { void api.cronTemplates().then(setTemplates).catch(() => {}); void api.containers().then((c) => setContainers(c.map((x) => x.name))).catch(() => {}); }, []);
  useEffect(() => { if (initial.id && initial.type === "script") void api.jobVersions(initial.id).then(setVersions).catch(() => {}); }, [initial.id, initial.type]);
  useEffect(() => {
    const t = setTimeout(() => { api.cronPreview(j.schedule ?? "", j.timezone ?? "").then((p) => setPreview(p)).catch((e) => setPreview({ described: "", next: [], error: e instanceof RequestError ? e.message : String(e) })); }, 300);
    return () => clearTimeout(t);
  }, [j.schedule, j.timezone]);

  const submit = async (e?: FormEvent) => {
    e?.preventDefault(); setBusy(true); setMsg(null);
    try { const saved = await api.jobSave(j); await onSaved(saved); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } finally { setBusy(false); }
  };
  const runLint = async () => { const r = await api.cronLint(j.script ?? ""); setLint(r.available ? r.output || "ShellCheck found nothing to report." : "ShellCheck is not installed on this server (apt install shellcheck)."); };
  const useTemplate = (id: string) => { const t = templates.find((x) => x.id === id); if (t) set({ type: "script", name: j.name || t.name, schedule: t.schedule, script: t.script }); };
  const restore = async (v: number) => { const r = await api.jobVersion(initial.id!, v); set({ script: r.content }); };
  const chain = (j.command ?? "").split(",").filter(Boolean);
  const t = TYPES[j.type ?? "command"];

  return (
    <Card title={j.id ? `Edit ${initial.name}` : "New job"} description={t?.help}>
      <form onSubmit={submit} className="grid gap-4 md:grid-cols-2">
        <Field label="Name"><Input value={j.name ?? ""} onChange={(e) => set({ name: e.target.value })} required placeholder="Nightly Postgres backup" /></Field>
        <Field label="Type"><select value={j.type} onChange={(e) => set({ type: e.target.value })} className={SELECT}>{Object.entries(TYPES).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}</select></Field>

        <div className="md:col-span-2 grid gap-3 rounded-md border border-border p-3 md:grid-cols-[1fr_1fr_200px]">
          <Field label={j.type === "heartbeat" ? "Expected schedule" : "Schedule"} hint="Five cron fields, or @hourly, @daily, @weekly. Leave empty for manual only.">
            <div className="flex gap-2"><Input value={j.schedule ?? ""} onChange={(e) => set({ schedule: e.target.value })} className="font-mono" placeholder="0 3 * * *" /><select value="" onChange={(e) => e.target.value && set({ schedule: e.target.value })} className={`${SELECT} w-36`}><option value="">Presets</option>{PRESETS.map(([l, s]) => <option key={s} value={s}>{l}</option>)}</select><Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => setBuilder(!builder)}>Build</Button></div>
            {builder && <ScheduleBuilder onPick={(s) => { set({ schedule: s }); setBuilder(false); }} />}
          </Field>
          <Field label="Timezone" hint="IANA name, empty means server time."><Input value={j.timezone ?? ""} onChange={(e) => set({ timezone: e.target.value })} placeholder="Europe/Skopje" /></Field>
          <div className="text-xs">
            <div className="mb-1 font-medium">{preview?.error ? <span className="text-danger">{preview.error}</span> : preview?.described}</div>
            {preview?.next.map((n) => <div key={n} className="text-ink-muted">{fmt(n)}</div>)}
          </div>
        </div>

        {j.type === "command" && <Field label="Command"><Input value={j.command ?? ""} onChange={(e) => set({ command: e.target.value })} className="font-mono md:col-span-2" placeholder="docker exec postgres pg_dump -U postgres app | gzip > /var/backups/app.sql.gz" /></Field>}
        {j.type === "file" && <Field label="Path" hint="Absolute path to an executable."><Input value={j.command ?? ""} onChange={(e) => set({ command: e.target.value })} className="font-mono" placeholder="/usr/local/bin/backup.sh" /></Field>}
        {j.type === "container" && <>
          <Field label="Container"><Input list="islet-containers" value={j.container ?? ""} onChange={(e) => set({ container: e.target.value })} placeholder="postgres" /><datalist id="islet-containers">{containers.map((c) => <option key={c} value={c} />)}</datalist></Field>
          <Field label="Command inside the container"><Input value={j.script ?? ""} onChange={(e) => set({ script: e.target.value })} className="font-mono" placeholder="pg_dump -U postgres app > /backups/app.sql" /></Field>
        </>}
        {j.type === "image" && <>
          <Field label="Image and arguments"><Input value={j.command ?? ""} onChange={(e) => set({ command: e.target.value })} className="font-mono" placeholder="-v /srv:/srv alpine:3" /></Field>
          <Field label="Command (optional)"><Input value={j.script ?? ""} onChange={(e) => set({ script: e.target.value })} className="font-mono" placeholder="du -sh /srv" /></Field>
        </>}
        {j.type === "http" && <>
          <Field label="URL"><Input value={j.command ?? ""} onChange={(e) => set({ command: e.target.value })} className="font-mono" placeholder="https://example.com/cron/tick" /></Field>
          <Field label="Method"><select value={j.httpMethod ?? "GET"} onChange={(e) => set({ httpMethod: e.target.value })} className={SELECT}>{["GET", "POST", "PUT", "DELETE"].map((m) => <option key={m}>{m}</option>)}</select></Field>
        </>}
        {j.type === "chain" && <div className="md:col-span-2">
          <span className="mb-1 block text-sm font-medium">Jobs to run, in order</span>
          <div className="flex flex-wrap gap-1">{jobs.filter((x) => x.id !== j.id && x.type !== "chain" && x.type !== "heartbeat").map((x) => { const on = chain.includes(x.id); return <button key={x.id} type="button" onClick={() => set({ command: (on ? chain.filter((c) => c !== x.id) : [...chain, x.id]).join(",") })} className={`rounded-sm border px-2 py-0.5 text-xs ${on ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{on && `${chain.indexOf(x.id) + 1}. `}{x.name}</button>; })}</div>
        </div>}
        {j.type === "heartbeat" && <Field label="Grace period (seconds)" hint="How late a ping may be before Islet alerts."><Input type="number" value={j.graceSec ?? 300} onChange={(e) => set({ graceSec: +e.target.value })} /></Field>}

        {j.type === "script" && <div className="md:col-span-2">
          <div className="mb-1 flex flex-wrap items-center justify-between gap-2">
            <span className="text-sm font-medium">Script</span>
            <div className="flex flex-wrap gap-2 text-xs">
              <select value="" onChange={(e) => e.target.value && useTemplate(e.target.value)} className="h-7 rounded-md border border-border-strong bg-bg px-2 text-xs"><option value="">Start from a template</option>{templates.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</select>
              <select value="" onChange={(e) => { const v = e.target.value.replace("#!", ""); if (v) set({ script: `#!${v}\n` + (j.script ?? "").replace(/^#!.*\n/, "") }); }} className="h-7 rounded-md border border-border-strong bg-bg px-2 text-xs"><option value="">Shebang</option><option value="#!/usr/bin/env bash">bash</option><option value="#!/bin/sh">sh</option><option value="#!/usr/bin/env python3">python3</option><option value="#!/usr/bin/env node">node</option></select>
              {versions.length > 0 && <select value="" onChange={(e) => e.target.value && void restore(+e.target.value)} className="h-7 rounded-md border border-border-strong bg-bg px-2 text-xs"><option value="">History ({versions.length})</option>{versions.map((v) => <option key={v.id} value={v.id}>{fmt(v.createdAt)} · {v.actor}</option>)}</select>}
              <button type="button" onClick={() => void runLint()} className="text-ink-muted hover:text-ink">ShellCheck</button>
            </div>
          </div>
          <div className="h-72 overflow-hidden rounded-md border border-border"><Suspense fallback={<div className="p-3 text-xs text-ink-muted">Loading editor…</div>}><CodeEditor name="job.sh" value={j.script ?? ""} onChange={(v) => set({ script: v })} onSave={() => void submit()} /></Suspense></div>
          {lint && <pre className="mt-2 max-h-40 overflow-auto rounded-md border border-border bg-bg p-2 font-mono text-xs whitespace-pre-wrap">{lint}</pre>}
        </div>}

        <div className="md:col-span-2"><button type="button" onClick={() => setAdvanced(!advanced)} className="text-xs text-ink-muted hover:text-ink">{advanced ? "Hide" : "Show"} advanced options</button></div>
        {advanced && j.type !== "heartbeat" && <>
          <Field label="Working directory"><Input value={j.workDir ?? ""} onChange={(e) => set({ workDir: e.target.value })} className="font-mono" placeholder="/srv/app" /></Field>
          <Field label="Run as user" hint="Linux only. Empty runs as the daemon user."><Input value={j.runAs ?? ""} onChange={(e) => set({ runAs: e.target.value })} placeholder="deploy" /></Field>
          <Field label="Timeout (seconds)"><Input type="number" value={j.timeoutSec ?? 3600} onChange={(e) => set({ timeoutSec: +e.target.value })} /></Field>
          <Field label="If the previous run is still going"><select value={j.overlap ?? "skip"} onChange={(e) => set({ overlap: e.target.value })} className={SELECT}><option value="skip">Skip this run</option><option value="queue">Wait, then run</option><option value="kill">Stop it and start over</option></select></Field>
          <Field label="Retries on failure" hint="30 seconds apart."><Input type="number" min={0} max={10} value={j.retries ?? 0} onChange={(e) => set({ retries: +e.target.value })} /></Field>
          <Field label="Random delay (seconds)" hint="Spreads load when many jobs share a schedule."><Input type="number" min={0} value={j.jitterSec ?? 0} onChange={(e) => set({ jitterSec: +e.target.value })} /></Field>
          <Field label="Nice level" hint="-20 (highest priority) to 19 (lowest). Linux only."><Input type="number" min={-20} max={19} value={j.nice ?? 0} onChange={(e) => set({ nice: +e.target.value })} /></Field>
          <Field label="Notify"><select value={j.notifyOn ?? "failure"} onChange={(e) => set({ notifyOn: e.target.value })} className={SELECT}><option value="failure">On failure and recovery</option><option value="always">After every run</option><option value="never">Never</option></select></Field>
        </>}
        <label className="flex items-center gap-1.5 text-sm md:col-span-2"><input type="checkbox" checked={j.enabled ?? true} onChange={(e) => set({ enabled: e.target.checked })} />Enabled</label>
        <div className="flex items-center gap-2 md:col-span-2"><Button type="submit" disabled={busy}>Save</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-danger">{msg}</span>}</div>
      </form>
    </Card>
  );
}

function ScheduleBuilder({ onPick }: { onPick: (s: string) => void }) {
  const [mode, setMode] = useState("daily");
  const [n, setN] = useState(15); const [hour, setHour] = useState(3); const [minute, setMinute] = useState(0);
  const [days, setDays] = useState<number[]>([1, 2, 3, 4, 5]); const [dom, setDom] = useState(1);
  const expr = mode === "minutes" ? `*/${n} * * * *` : mode === "hourly" ? `${minute} * * * *` : mode === "daily" ? `${minute} ${hour} * * *` : mode === "weekly" ? `${minute} ${hour} * * ${[...days].sort().join(",") || "*"}` : `${minute} ${hour} ${dom} * *`;
  const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const num = (v: number, set: (x: number) => void, min: number, max: number, w = "w-16") => <Input type="number" min={min} max={max} value={v} onChange={(e) => set(+e.target.value)} className={`${w} font-mono`} />;
  return (
    <div className="mt-2 flex flex-wrap items-end gap-2 rounded-md border border-border bg-surface-2 p-2 text-xs">
      <select value={mode} onChange={(e) => setMode(e.target.value)} className="h-8 rounded-md border border-border-strong bg-bg px-2 text-xs"><option value="minutes">Every N minutes</option><option value="hourly">Every hour</option><option value="daily">Every day</option><option value="weekly">On certain days</option><option value="monthly">Once a month</option></select>
      {mode === "minutes" && <span className="flex items-center gap-1">every {num(n, setN, 1, 59)} min</span>}
      {mode !== "minutes" && <span className="flex items-center gap-1">at {mode !== "hourly" && num(hour, setHour, 0, 23)}{mode !== "hourly" && ":"}{num(minute, setMinute, 0, 59)}{mode === "hourly" && " minutes past"}</span>}
      {mode === "weekly" && <span className="flex gap-1">{DOW.map((d, i) => <button key={d} type="button" onClick={() => setDays(days.includes(i) ? days.filter((x) => x !== i) : [...days, i])} className={`rounded-sm border px-1.5 py-0.5 ${days.includes(i) ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{d}</button>)}</span>}
      {mode === "monthly" && <span className="flex items-center gap-1">on day {num(dom, setDom, 1, 28)}</span>}
      <span className="font-mono text-ink-muted">{expr}</span>
      <Button type="button" className="h-8 text-xs" onClick={() => onPick(expr)}>Use</Button>
    </div>
  );
}

function ImportPanel({ onClose, onDone }: { onClose: () => void; onDone: () => Promise<void> }) {
  const [text, setText] = useState("");
  const [found, setFound] = useState<Job[] | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [source, setSource] = useState("crontab");
  const preview = async () => { setMsg(null); try { const j = await api.cronImport(text, false, source); setFound(j); if (j.length === 0) setMsg(source === "systemd" ? "No systemd timers with an OnCalendar schedule found under /etc/systemd/system." : text ? "No valid entries found." : "No system crontab entries found. Paste one below."); } catch (e) { setMsg(e instanceof RequestError ? e.message : String(e)); } };
  const save = async () => { await api.cronImport(text, true, source); await onDone(); };
  return (
    <Card title="Import jobs" description="Reads root's crontab and /etc/cron.d, systemd timers under /etc/systemd/system, or paste crontab text. Imported jobs start paused so nothing runs twice; disable the originals, then resume them here.">
      <div className="mb-2 flex gap-1 text-xs">{[["crontab", "Crontab"], ["systemd", "systemd timers"]].map(([k, l]) => <button key={k} type="button" onClick={() => { setSource(k); setFound(null); }} className={`rounded-sm border px-2 py-0.5 ${source === k ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{l}</button>)}</div>
      {source === "crontab" && <textarea value={text} onChange={(e) => setText(e.target.value)} rows={5} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder="0 3 * * * /usr/local/bin/backup.sh" />}
      <div className="mt-2 flex items-center gap-2"><Button variant="secondary" className="h-8 text-xs" onClick={() => void preview()}>Preview</Button>{found && found.length > 0 && <Button className="h-8 text-xs" onClick={() => void save()}>Import {found.length} job{found.length === 1 ? "" : "s"}</Button>}<Button variant="secondary" className="h-8 text-xs" onClick={onClose}>Cancel</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>
      {found && found.length > 0 && <ul className="mt-3 divide-y divide-border text-xs">{found.map((j, i) => <li key={i} className="py-1.5"><span className="font-mono">{j.schedule}</span> <span className="text-ink-muted">{j.command}</span></li>)}</ul>}
    </Card>
  );
}
