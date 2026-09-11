import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, RequestError, type BackupDestination, type BackupOverview, type BackupPlan, type BackupRun, type BackupSource, type Snapshot } from "@/lib/api";
import { postStream } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";

const SELECT = "h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm";
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function bytes(n: number) { return n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : n < 1073741824 ? `${(n / 1048576).toFixed(1)} MB` : `${(n / 1073741824).toFixed(2)} GB`; }
function err(e: unknown) { return e instanceof RequestError ? e.message : e instanceof Error ? e.message : String(e); }
const DEST_TYPES: Record<string, { label: string; fields: { key: string; label: string; secret?: boolean; hint?: string }[]; help: string }> = {
  s3: { label: "S3-compatible", help: "AWS S3, Cloudflare R2, Backblaze B2, Wasabi, Hetzner Object Storage, MinIO. Use write-only credentials where the provider offers them.", fields: [{ key: "endpoint", label: "Endpoint", hint: "s3.eu-central-1.amazonaws.com or <account>.r2.cloudflarestorage.com" }, { key: "bucket", label: "Bucket" }, { key: "prefix", label: "Prefix (optional)" }, { key: "region", label: "Region (optional)" }, { key: "accessKey", label: "Access key" }, { key: "secretKey", label: "Secret key", secret: true }] },
  sftp: { label: "SFTP", help: "Any SSH server, including a Hetzner Storage Box (host uXXXX.your-storagebox.de, port 23).", fields: [{ key: "host", label: "Host" }, { key: "port", label: "Port", hint: "22" }, { key: "user", label: "User" }, { key: "path", label: "Path on the server", hint: "/backups/islet" }, { key: "privateKey", label: "Private key (OpenSSH)", secret: true }] },
  local: { label: "Local path", help: "A folder on this server or an attached volume. Fast restores, but not off-site.", fields: [{ key: "path", label: "Absolute path", hint: "/mnt/backup/islet" }] },
  rest: { label: "REST server", help: "A restic rest-server, for example on another Islet box.", fields: [{ key: "url", label: "URL", hint: "https://backup.example.com:8000/islet" }, { key: "user", label: "User (optional)" }, { key: "password", label: "Password (optional)", secret: true }] },
};
const PRESETS: { label: string; schedule: string; keep: [number, number, number, number] }[] = [
  { label: "Everything nightly (recommended)", schedule: "0 3 * * *", keep: [7, 4, 6, 1] },
  { label: "Databases hourly", schedule: "15 * * * *", keep: [24, 7, 4, 0] },
  { label: "Config weekly", schedule: "0 4 * * 0", keep: [0, 8, 12, 2] },
];

export default function Backups() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [o, setO] = useState<BackupOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editDest, setEditDest] = useState<Partial<BackupDestination> | null>(null);
  const [editPlan, setEditPlan] = useState<Partial<BackupPlan> | null>(null);
  const [selPlan, setSelPlan] = useState<string | null>(null);
  const [browse, setBrowse] = useState<string | null>(null);
  const load = useCallback(() => api.backups().then((x) => { setO(x); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); const id = setInterval(() => void load(), 20000); return () => clearInterval(id); }, [load]);
  if (error) return <div className="mx-auto max-w-6xl"><Alert>{error}</Alert></div>;
  if (!o) return <p className="text-sm text-ink-muted">Loading…</p>;
  const h = o.health;
  const plan = o.plans.find((p) => p.id === selPlan);

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Backups</h1>
          <p className="mt-1 text-ink-muted">Encrypted, deduplicated snapshots with restic. Nothing to install: it runs in a container.</p>
        </div>
        {isAdmin && o.destinations.length > 0 && <a href="/api/v1/backups/kit" className="text-sm text-ink-muted hover:text-ink">Download recovery kit</a>}
      </div>
      {o.plans.length > 0 && !o.kitDownloadedAt && isAdmin && <Alert tone="warning">The recovery kit has never been downloaded. Without it, these encrypted backups are unreadable once this server is gone. <a href="/api/v1/backups/kit" className="underline">Download it now</a> and keep it somewhere else.</Alert>}
      {o.plans.length === 0 && <Alert tone="warning">No backup plan yet. Add a destination, then a plan. "Everything nightly" takes two minutes.</Alert>}
      {o.plans.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-5">
          {[["Last success", h.lastSuccess ? fmt(h.lastSuccess) : "never"], ["Next run", h.nextRun ? fmt(h.nextRun) : "—"], ["Last verified", h.lastVerified ? fmt(h.lastVerified) : "not yet"], ["Restore tested", h.lastRestoreTest ? fmt(h.lastRestoreTest) : "not yet"], ["Attention", h.stale + h.failed === 0 ? "none" : `${h.stale} stale, ${h.failed} failed`]].map(([k, v]) => <div key={k} className={`rounded-lg border border-border bg-surface p-3 ${k === "Attention" && h.stale + h.failed > 0 ? "border-danger/40" : ""}`}><div className="text-xs text-ink-muted">{k}</div><div className="mt-1 text-sm font-medium">{v}</div></div>)}
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="Destinations" description="One encrypted repository each. The key is generated by Islet and included in the recovery kit.">
          <ul className="divide-y divide-border text-sm">
            {o.destinations.map((d) => (
              <li key={d.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
                <div><span className="font-medium">{d.name}</span> <span className="ml-1 font-mono text-[11px] text-ink-muted">{d.repo}</span><div className="text-xs text-ink-muted">{d.lastCheck ? `${d.checkOk ? "verified" : "check FAILED"} ${fmt(d.lastCheck)}` : "not verified yet"}{d.size > 0 && ` · ${bytes(d.size)} stored · about $${(d.size / 1073741824 * 0.006).toFixed(2)}/month at typical object storage prices`}{d.lastRestoreTest && ` · restore test ${d.restoreTestOk ? "passed" : "FAILED"} ${fmt(d.lastRestoreTest)}`}</div></div>
                <div className="flex gap-3 text-xs"><button type="button" onClick={() => setBrowse(browse === d.id ? null : d.id)} className="text-ink-muted hover:text-ink">Snapshots</button>{isAdmin && <><button type="button" onClick={async () => { try { await api.destinationVerify(d.id); await load(); } catch (e) { alert(err(e)); } }} className="text-ink-muted hover:text-ink">Verify</button><button type="button" onClick={async () => { try { const r = await api.destinationRestoreTest(d.id); alert(r.output); await load(); } catch (e) { alert(err(e)); } }} className="text-ink-muted hover:text-ink">Restore test</button><button type="button" onClick={() => setEditDest({ ...d })} className="text-ink-muted hover:text-ink">Edit</button><button type="button" onClick={async () => { if (confirm(`Remove destination ${d.name}? Snapshots stay in the repository.`)) { try { await api.destinationDelete(d.id); await load(); } catch (e) { alert(err(e)); } } }} className="text-danger hover:underline">Remove</button></>}</div>
              </li>
            ))}
            {o.destinations.length === 0 && <li className="py-2 text-ink-muted">None yet.</li>}
          </ul>
          {isAdmin && !editDest && <Button variant="secondary" className="mt-3 h-8 text-xs" onClick={() => setEditDest({ id: "", type: "s3", name: "", config: {} })}>Add destination</Button>}
          {editDest && <DestForm initial={editDest} onClose={() => setEditDest(null)} onSaved={async () => { setEditDest(null); await load(); }} />}
        </Card>
        <Card title="Plans" description="Sources, destination, schedule and retention.">
          <ul className="divide-y divide-border text-sm">
            {o.plans.map((p) => (
              <li key={p.id} className={`cursor-pointer py-2 ${selPlan === p.id ? "text-ink" : ""}`} onClick={() => setSelPlan(p.id)}>
                <div className="flex items-center justify-between"><div className="flex items-center gap-2"><span className={`h-2 w-2 rounded-full ${!p.enabled ? "bg-ink-faint" : p.running ? "bg-accent animate-pulse" : p.stale || p.lastStatus === "failed" ? "bg-danger" : p.lastStatus === "success" ? "bg-success" : "bg-warning"}`} /><span className="font-medium">{p.name}</span></div><span className="text-xs text-ink-muted">{p.described}</span></div>
                <div className="ml-4 text-xs text-ink-muted">{p.sources.map((s) => s.type === "islet" ? "Islet state" : `${s.type} ${s.value}`).join(", ")} → {o.destinations.find((d) => d.id === p.destinationId)?.name ?? "?"} · {p.lastRunAt ? `last ${p.lastStatus} ${fmt(p.lastRunAt)}` : "never run"}</div>
              </li>
            ))}
            {o.plans.length === 0 && <li className="py-2 text-ink-muted">None yet.</li>}
          </ul>
          {isAdmin && !editPlan && <Button className="mt-3 h-8 text-xs" disabled={o.destinations.length === 0} onClick={() => setEditPlan({ id: "", name: "nightly", destinationId: o.destinations[0]?.id ?? "", sources: [{ type: "islet", value: "" }], schedule: "0 3 * * *", keepDaily: 7, keepWeekly: 4, keepMonthly: 6, keepYearly: 1, enabled: true })}>New plan</Button>}
          {editPlan && <PlanForm initial={editPlan} o={o} onClose={() => setEditPlan(null)} onSaved={async (p) => { setEditPlan(null); await load(); setSelPlan(p.id); }} />}
        </Card>
      </div>
      {plan && <PlanDetail plan={plan} isAdmin={isAdmin} onChanged={load} onEdit={() => setEditPlan({ ...plan })} />}
      {browse && <SnapshotBrowser destId={browse} isAdmin={isAdmin} volumes={o.volumes} />}
    </div>
  );
}

function DestForm({ initial, onClose, onSaved }: { initial: Partial<BackupDestination>; onClose: () => void; onSaved: () => Promise<void> }) {
  const [d, setD] = useState<Partial<BackupDestination>>(initial);
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const t = DEST_TYPES[d.type ?? "s3"];
  const setCfg = (k: string, v: string) => setD((c) => ({ ...c, config: { ...(c.config ?? {}), [k]: v } }));
  const submit = async (e: FormEvent) => { e.preventDefault(); setBusy(true); setMsg("Connecting and initialising the repository…"); try { await api.destinationSave(d); await onSaved(); } catch (er) { setMsg(err(er)); } finally { setBusy(false); } };
  return (
    <form onSubmit={submit} className="mt-3 grid gap-3 border-t border-border pt-3 sm:grid-cols-2">
      <Field label="Name"><Input value={d.name ?? ""} onChange={(e) => setD({ ...d, name: e.target.value })} required disabled={!!d.id} placeholder="hetzner-box" /></Field>
      <Field label="Type"><select value={d.type} onChange={(e) => setD({ ...d, type: e.target.value as BackupDestination["type"], config: {} })} className={SELECT} disabled={!!d.id}>{Object.entries(DEST_TYPES).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}</select></Field>
      <p className="text-xs text-ink-muted sm:col-span-2">{t.help}</p>
      {t.fields.map((f) => <Field key={f.key} label={f.label} hint={d.id && f.secret ? "Leave empty to keep the stored value." : f.hint}>{f.key === "privateKey" ? <textarea value={d.config?.[f.key] ?? ""} onChange={(e) => setCfg(f.key, e.target.value)} rows={4} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" /> : <Input type={f.secret ? "password" : "text"} value={d.config?.[f.key] ?? ""} onChange={(e) => setCfg(f.key, e.target.value)} placeholder={f.hint} autoComplete="off" className="font-mono" />}</Field>)}
      <div className="flex items-center gap-2 sm:col-span-2"><Button type="submit" disabled={busy}>{d.id ? "Save" : "Add and initialise"}</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>
    </form>
  );
}

function PlanForm({ initial, o, onClose, onSaved }: { initial: Partial<BackupPlan>; o: BackupOverview; onClose: () => void; onSaved: (p: BackupPlan) => Promise<void> }) {
  const [p, setP] = useState<Partial<BackupPlan>>(initial);
  const [msg, setMsg] = useState<string | null>(null);
  const [path, setPath] = useState("");
  const set = (x: Partial<BackupPlan>) => setP((c) => ({ ...c, ...x }));
  const srcs = p.sources ?? [];
  const has = (s: BackupSource) => srcs.some((x) => x.type === s.type && x.value === s.value);
  const toggle = (s: BackupSource) => set({ sources: has(s) ? srcs.filter((x) => !(x.type === s.type && x.value === s.value)) : [...srcs, s] });
  const submit = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await onSaved(await api.planSave(p)); } catch (er) { setMsg(err(er)); } };
  const chip = (s: BackupSource, label: string) => <button key={s.type + s.value} type="button" onClick={() => toggle(s)} className={`rounded-sm border px-2 py-0.5 text-xs ${has(s) ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{label}</button>;
  return (
    <form onSubmit={submit} className="mt-3 grid gap-3 border-t border-border pt-3 sm:grid-cols-2">
      <Field label="Name"><Input value={p.name ?? ""} onChange={(e) => set({ name: e.target.value })} required /></Field>
      <Field label="Destination"><select value={p.destinationId} onChange={(e) => set({ destinationId: e.target.value })} className={SELECT}>{o.destinations.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}</select></Field>
      <div className="sm:col-span-2">
        <span className="mb-1 block text-sm font-medium">Sources</span>
        <div className="flex flex-wrap gap-1">{chip({ type: "islet", value: "" }, "Islet state (database, secrets, scripts, certificates)")}{(o.databases ?? []).map((n) => chip({ type: "database", value: n }, `database ${n}`))}{o.volumes.map((v) => chip({ type: "volume", value: v }, `volume ${v}`))}{srcs.filter((s) => s.type === "path").map((s) => chip(s, `path ${s.value}`))}</div>
        <div className="mt-2 flex gap-2"><Input value={path} onChange={(e) => setPath(e.target.value)} className="font-mono" placeholder="/srv/uploads" /><Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => { if (path.startsWith("/")) { toggle({ type: "path", value: path }); setPath(""); } }}>Add path</Button></div>
      </div>
      <Field label="Preset"><select value="" onChange={(e) => { const pr = PRESETS[+e.target.value]; if (pr) set({ schedule: pr.schedule, keepDaily: pr.keep[0], keepWeekly: pr.keep[1], keepMonthly: pr.keep[2], keepYearly: pr.keep[3] }); }} className={SELECT}><option value="">Pick a preset…</option>{PRESETS.map((pr, i) => <option key={pr.label} value={i}>{pr.label}</option>)}</select></Field>
      <Field label="Schedule" hint="Cron fields, server time."><Input value={p.schedule ?? ""} onChange={(e) => set({ schedule: e.target.value })} className="font-mono" /></Field>
      <div className="grid grid-cols-4 gap-2 sm:col-span-2">{(["keepDaily", "keepWeekly", "keepMonthly", "keepYearly"] as const).map((k) => <Field key={k} label={`Keep ${k.replace("keep", "").toLowerCase()}`}><Input type="number" min={0} value={p[k] ?? 0} onChange={(e) => set({ [k]: +e.target.value })} /></Field>)}</div>
      <p className="text-xs text-ink-muted sm:col-span-2">Keeps the last {p.keepDaily} daily, {p.keepWeekly} weekly, {p.keepMonthly} monthly and {p.keepYearly} yearly snapshots: at most {(p.keepDaily ?? 0) + (p.keepWeekly ?? 0) + (p.keepMonthly ?? 0) + (p.keepYearly ?? 0)} snapshots. Deduplication means unchanged data is stored once.</p>
      <label className="flex items-center gap-1.5 text-sm sm:col-span-2"><input type="checkbox" checked={p.enabled ?? true} onChange={(e) => set({ enabled: e.target.checked })} />Enabled</label>
      <div className="flex items-center gap-2 sm:col-span-2"><Button type="submit">{p.id ? "Save" : "Create plan"}</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-danger">{msg}</span>}</div>
    </form>
  );
}

function PlanDetail({ plan, isAdmin, onChanged, onEdit }: { plan: BackupPlan; isAdmin: boolean; onChanged: () => Promise<void>; onEdit: () => void }) {
  const [runs, setRuns] = useState<BackupRun[]>([]);
  const [live, setLive] = useState<string[] | null>(null);
  const [open, setOpen] = useState<BackupRun | null>(null);
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLPreElement>(null);
  const loadRuns = useCallback(() => api.planRuns(plan.id).then(setRuns).catch(() => {}), [plan.id]);
  useEffect(() => { void loadRuns(); setOpen(null); setLive(null); }, [loadRuns]);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [live]);
  const run = async () => { setBusy(true); setLive([]); setOpen(null); try { await postStream(`/api/v1/backups/plans/${plan.id}/run`, (l) => setLive((p) => [...(p ?? []), l])); } catch (e) { setLive((p) => [...(p ?? []), `[islet] ${err(e)}`]); } finally { setBusy(false); await loadRuns(); await onChanged(); } };
  return (
    <Card title={plan.name} description={`${plan.described} · next ${plan.nextRunAt ? fmt(plan.nextRunAt) : "paused"}`}>
      <div className="flex flex-wrap items-center gap-2">
        {isAdmin && <Button className="h-8 text-xs" disabled={busy || plan.running} onClick={() => void run()}>{busy || plan.running ? "Running…" : "Back up now"}</Button>}
        {isAdmin && (busy || plan.running) && <Button variant="danger" className="h-8 text-xs" onClick={() => void api.planCancel(plan.id)}>Cancel</Button>}
        {isAdmin && <button type="button" onClick={onEdit} className="text-xs text-ink-muted hover:text-ink">Edit plan</button>}
        {isAdmin && <button type="button" onClick={async () => { if (confirm(`Delete plan ${plan.name}? Snapshots stay in the repository.`)) { await api.planDelete(plan.id); await onChanged(); } }} className="ml-auto text-xs text-danger hover:underline">Delete plan</button>}
      </div>
      {(live || open) && <div className="mt-3"><div className="mb-1 flex items-center justify-between text-xs text-ink-muted"><span>{open ? `Run #${open.id} · ${open.status} · ${open.trigger} · ${fmt(open.startedAt)}` : "Live output"}</span><button type="button" onClick={() => { setLive(null); setOpen(null); }} className="hover:text-ink">Close</button></div><pre ref={box} className="max-h-72 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{open ? open.log || "(no log)" : (live ?? []).join("\n") || "starting…"}</pre></div>}
      <table className="mt-3 w-full text-xs"><tbody className="divide-y divide-border">
        {runs.map((r) => <tr key={r.id} className="cursor-pointer hover:bg-surface-2" onClick={async () => { setLive(null); setOpen(await api.planRun(plan.id, r.id)); }}><td className="py-1.5"><span className={r.status === "success" ? "text-success" : r.status === "failed" ? "text-danger" : "text-accent"}>{r.status}</span></td><td className="py-1.5 font-mono">{r.snapshot}</td><td className="py-1.5 text-ink-muted">{r.status === "success" ? `${bytes(r.bytesAdded)} added of ${bytes(r.bytesTotal)} · ${r.filesNew} new, ${r.filesChanged} changed` : r.error}</td><td className="py-1.5 text-right text-ink-muted whitespace-nowrap">{r.trigger} · {fmt(r.startedAt)}{r.durationMs > 0 && ` · ${Math.round(r.durationMs / 1000)} s`}</td></tr>)}
        {runs.length === 0 && <tr><td className="py-2 text-ink-muted">No runs yet. Press Run now; the first snapshot uploads everything, later ones only what changed.</td></tr>}
      </tbody></table>
    </Card>
  );
}

function SnapshotBrowser({ destId, isAdmin, volumes }: { destId: string; isAdmin: boolean; volumes: string[] }) {
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [sel, setSel] = useState<string | null>(null);
  const [path, setPath] = useState("/data");
  const [entries, setEntries] = useState<{ path: string; name: string; type: string; size?: number }[]>([]);
  useEffect(() => { setSnaps(null); setMsg("Listing snapshots…"); api.snapshots(destId).then((s) => { setSnaps(s); setMsg(null); }).catch((e) => setMsg(err(e))); }, [destId]);
  useEffect(() => { if (!sel) return; api.snapshotLs(destId, sel, path).then(setEntries).catch((e) => setMsg(err(e))); }, [destId, sel, path]);
  const restore = async (include: string) => {
    const isVol = include.startsWith("/data/volumes/") && include.split("/").length === 4;
    const vol = isVol ? prompt(`Restore ${include} into a NEW Docker volume named:`, include.split("/")[3] + "-restored") : "";
    if (isVol && !vol) return;
    if (!isVol && !confirm(`Restore ${include} into a folder under the Islet data directory?`)) return;
    setMsg("Restoring…");
    try { const r = await api.restore(destId, { snapshot: sel!, include, newVolume: vol ?? "" }); setMsg(`Restored to ${r.target}`); } catch (e) { setMsg(err(e)); }
  };
  return (
    <Card title="Snapshots" description="Browse a snapshot and restore a volume into a new volume, or any folder to disk. Live data is never overwritten.">
      {msg && <p className="mb-2 text-xs text-ink-muted">{msg}</p>}
      <div className="grid gap-4 md:grid-cols-[260px_minmax(0,1fr)]">
        <ul className="max-h-72 divide-y divide-border overflow-auto text-xs">{(snaps ?? []).map((s) => <li key={s.id}><button type="button" onClick={() => { setSel(s.id); setPath("/data"); }} className={`w-full py-1.5 text-left ${sel === s.id ? "text-ink" : "text-ink-muted hover:text-ink"}`}><span className="font-mono">{s.id}</span> · {fmt(s.time)}{s.size > 0 && ` · ${bytes(s.size)}`}<div className="text-ink-faint">{s.tags.filter((t) => t.startsWith("plan:")).join(" ")}</div></button></li>)}{snaps && snaps.length === 0 && <li className="py-2 text-ink-muted">Empty repository.</li>}</ul>
        <div className="text-xs">
          {sel && <div className="mb-1 flex items-center gap-2 font-mono"><button type="button" onClick={() => setPath(path.split("/").slice(0, -1).join("/") || "/data")} disabled={path === "/data"} className="text-ink-muted hover:text-ink disabled:opacity-40">↑</button>{path}</div>}
          <ul className="divide-y divide-border">{entries.map((e) => <li key={e.path} className="flex items-center justify-between py-1"><button type="button" onClick={() => e.type === "dir" && setPath(e.path)} className={e.type === "dir" ? "hover:underline" : "cursor-default"}>{e.type === "dir" ? "📁 " : ""}{e.name}</button><span className="flex gap-3 text-ink-muted">{e.size !== undefined && e.type !== "dir" && bytes(e.size)}{isAdmin && <button type="button" onClick={() => void restore(e.path)} className="hover:text-ink">Restore</button>}</span></li>)}{sel && entries.length === 0 && <li className="py-1 text-ink-muted">Empty.</li>}{!sel && <li className="py-1 text-ink-muted">Pick a snapshot.</li>}</ul>
          {volumes.length === 0 && null}
        </div>
      </div>
    </Card>
  );
}
