import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api, RequestError, type Check, type CheckResult, type StatusPageSettings } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { pollInterval } from "@/lib/poll";
import { useDialog } from "@/lib/dialogs";

function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function pct(v: number) { return v < 0 ? "—" : v >= 99.995 ? "100%" : `${v.toFixed(2)}%`; }
function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }
const blank = (): Partial<Check> => ({ id: "", name: "", type: "http", target: "https://", keyword: "", intervalSec: 60, timeoutSec: 10, expectStatus: 0, enabled: true, public: false });

export default function Uptime() {
  const ask = useDialog();
  const { state } = useAuth();
  const canEdit = state.status === "authed" && state.me.user.role !== "viewer";
  const [params, setParams] = useSearchParams();
  const [checks, setChecks] = useState<Check[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<Check> | null>(null);
  const selected = params.get("c");
  const load = useCallback(() => api.checks().then((c) => { setChecks(c); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); const stop = pollInterval(() => void load(), 15000); return stop; }, [load]);

  const remove = async (c: Check) => { if (!(await ask.confirm({ title: `Delete the check ${c.name}?`, body: "Its history and uptime figures go with it.", confirmLabel: "Delete check", tone: "danger" }))) return; await api.checkDelete(c.id); if (selected === c.id) setParams({}); await load(); };
  const down = checks.filter((c) => c.status === "down").length;
  const sel = checks.find((c) => c.id === selected);
  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="sr-only">Uptime</h1>
          <p className="mt-1 text-ink-muted">{checks.length === 0 ? "Checks run from this server every minute." : down === 0 ? `All ${checks.length} checks are up.` : `${down} of ${checks.length} checks are down.`}</p>
        </div>
        {canEdit && <Button className="h-8 text-xs" onClick={() => setEditing(blank())}>New check</Button>}
      </div>
      {error && <Alert>{error}</Alert>}
      {canEdit && <StatusPage />}
      {editing && <CheckForm initial={editing} onClose={() => setEditing(null)} onSaved={async (c) => { setEditing(null); await load(); setParams({ c: c.id }); }} />}

      <div className="overflow-x-auto rounded-lg border border-border bg-surface">
        <table className="w-full min-w-[720px] text-sm">
          <thead className="text-left text-xs text-ink-muted"><tr className="border-b border-border"><th className="px-4 py-2 font-medium">Check</th><th className="px-4 py-2 font-medium">Status</th><th className="px-4 py-2 font-medium">Latency</th><th className="px-4 py-2 font-medium">24 h</th><th className="px-4 py-2 font-medium">30 d</th><th className="px-4 py-2" /></tr></thead>
          <tbody className="divide-y divide-border">
            {checks.map((c) => (
              <tr key={c.id} className={`cursor-pointer ${selected === c.id ? "bg-surface-2" : "hover:bg-surface-2"}`} onClick={() => setParams({ c: c.id })}>
                <td className="px-4 py-2.5"><div className="flex items-center gap-2"><span className={`h-2 w-2 rounded-full ${!c.enabled ? "bg-ink-faint" : c.status === "up" ? "bg-success" : c.status === "down" ? "bg-danger" : "bg-warning"}`} /><span className="font-medium">{c.name}</span><span className="font-mono text-[11px] text-ink-faint">{c.type} · {c.target}</span></div></td>
                <td className="px-4 py-2.5 text-xs">{!c.enabled ? <span className="text-ink-muted">paused</span> : c.status === "down" ? <span className="text-danger">down since {fmt(c.downSince)}</span> : c.status === "up" ? <span className="text-success">up</span> : <span className="text-ink-muted">waiting for first probe</span>}{c.lastError && c.status !== "up" && <div className="text-ink-muted">{c.lastError}</div>}</td>
                <td className="px-4 py-2.5 font-mono text-xs">{c.lastCheckAt ? `${c.lastLatencyMs} ms` : ""}</td>
                <td className="px-4 py-2.5 font-mono text-xs">{pct(c.uptime24h)}</td>
                <td className="px-4 py-2.5 font-mono text-xs">{pct(c.uptime30d)}</td>
                <td className="px-4 py-2.5 text-right text-xs whitespace-nowrap" onClick={(e) => e.stopPropagation()}>{canEdit && <><button type="button" onClick={() => setEditing({ ...c })} className="-my-1 py-1 text-ink-muted hover:text-ink">Edit</button><button type="button" onClick={() => void remove(c)} className="-my-1 ml-3 py-1 text-danger hover:underline">Delete</button></>}</td>
              </tr>
            ))}
            {checks.length === 0 && <tr><td colSpan={6} className="px-4 py-8 text-center text-ink-muted">No checks yet. Add your site, an API endpoint, or a database port.</td></tr>}
          </tbody>
        </table>
      </div>
      {sel && <CheckDetail check={sel} canEdit={canEdit} />}
    </div>
  );
}

function CheckDetail({ check, canEdit }: { check: Check; canEdit: boolean }) {
  const [results, setResults] = useState<CheckResult[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  const load = useCallback(() => api.checkResults(check.id, 200).then(setResults).catch(() => {}), [check.id]);
  useEffect(() => { void load(); setMsg(null); }, [load]);
  const probe = async () => { setMsg("Probing…"); try { const r = await api.checkProbe(check.id); setMsg(r.ok ? `OK in ${r.latencyMs} ms` : `Failed: ${r.error}`); } catch (e) { setMsg(err(e)); } };
  const pts = [...results].reverse();
  const max = Math.max(1, ...pts.map((r) => r.latencyMs));
  const incidents = results.filter((r) => !r.ok).slice(0, 10);
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
      <Card title={check.name} description={`${check.type.toUpperCase()} · every ${check.intervalSec}s · timeout ${check.timeoutSec}s${check.expectStatus ? ` · expects ${check.expectStatus}` : ""}${check.keyword ? ` · keyword "${check.keyword}"` : ""}`}>
        {canEdit && <div className="mb-3 flex items-center gap-3"><Button variant="secondary" className="h-8 text-xs" onClick={() => void probe()}>Probe now</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>}
        <div className="text-xs text-ink-muted">Latency, last {pts.length} probes · max {max} ms</div>
        <div className="mt-1 flex h-16 items-end gap-px">{pts.map((r, i) => <div key={i} title={`${fmt(r.at)} · ${r.ok ? `${r.latencyMs} ms` : r.error}`} className={`flex-1 rounded-sm ${r.ok ? "bg-success/70" : "bg-danger"}`} style={{ height: `${r.ok ? Math.max(4, (r.latencyMs / max) * 100) : 100}%` }} />)}{pts.length === 0 && <span className="text-xs text-ink-faint">No probes yet; the first one runs within a minute.</span>}</div>
      </Card>
      <Card title="Recent failures">
        <ul className="divide-y divide-border text-xs">{incidents.map((r, i) => <li key={i} className="py-1.5"><div className="text-danger">{r.error}</div><div className="text-ink-muted">{fmt(r.at)}</div></li>)}{incidents.length === 0 && <li className="py-2 text-ink-muted">None in the recent history.</li>}</ul>
      </Card>
    </div>
  );
}

function CheckForm({ initial, onClose, onSaved }: { initial: Partial<Check>; onClose: () => void; onSaved: (c: Check) => Promise<void> }) {
  const [c, setC] = useState<Partial<Check>>(initial);
  const [msg, setMsg] = useState<string | null>(null);
  const set = (p: Partial<Check>) => setC((x) => ({ ...x, ...p }));
  const submit = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await onSaved(await api.checkSave(c)); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title={c.id ? `Edit ${initial.name}` : "New check"} description="Two failures in a row count as down and raise a critical event; the recovery is reported too.">
      <form onSubmit={submit} className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <Field label="Name"><Input value={c.name ?? ""} onChange={(e) => set({ name: e.target.value })} required placeholder="Marketing site" /></Field>
        <Field label="Type"><Select value={c.type} onChange={(e) => set({ type: e.target.value as Check["type"], target: e.target.value === "tcp" ? "" : (c.target || "https://") })}><option value="http">HTTP status</option><option value="keyword">HTTP keyword</option><option value="tcp">TCP port</option></Select></Field>
        <Field label={c.type === "tcp" ? "Host and port" : "URL"}><Input value={c.target ?? ""} onChange={(e) => set({ target: e.target.value })} className="font-mono" placeholder={c.type === "tcp" ? "db.example.com:5432" : "https://example.com/health"} required /></Field>
        {c.type === "keyword" && <Field label="Keyword that must appear"><Input value={c.keyword ?? ""} onChange={(e) => set({ keyword: e.target.value })} required /></Field>}
        {c.type !== "tcp" && <Field label="Expected status" hint="0 accepts any status below 400."><Input type="number" value={c.expectStatus ?? 0} onChange={(e) => set({ expectStatus: +e.target.value })} /></Field>}
        <Field label="Interval (seconds)"><Input type="number" min={20} value={c.intervalSec ?? 60} onChange={(e) => set({ intervalSec: +e.target.value })} /></Field>
        <Field label="Timeout (seconds)"><Input type="number" min={1} max={60} value={c.timeoutSec ?? 10} onChange={(e) => set({ timeoutSec: +e.target.value })} /></Field>
        <label className="flex items-center gap-1.5 text-sm md:col-span-3"><input type="checkbox" checked={c.enabled ?? true} onChange={(e) => set({ enabled: e.target.checked })} />Enabled</label>
        <label className="flex items-start gap-1.5 text-sm md:col-span-3">
          <input type="checkbox" className="mt-1" checked={c.public ?? false} onChange={(e) => set({ public: e.target.checked })} />
          <span>Show on the public status page<span className="block text-xs text-ink-muted">Only the name and whether it answered. What it points at is never published.</span></span>
        </label>
        <div className="flex items-center gap-2 md:col-span-3"><Button type="submit">Save</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-danger">{msg}</span>}</div>
      </form>
    </Card>
  );
}

/**
 * The page strangers read.
 *
 * It lives here because it is made of these checks and nothing else, and
 * because the question it answers — "is it me or is it them" — is the one a
 * customer asks while the operator is looking at this very page.
 */
function StatusPage() {
  const [set, setSet] = useState<StatusPageSettings | null>(null);
  const [routed, setRouted] = useState(true);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(async () => {
    try { const r = await api.statusPage(); setSet(r.settings); setRouted(r.hostRouted); } catch { /* the card stays closed rather than shouting */ }
  }, []);
  useEffect(() => { void load(); }, [load]);
  if (!set) return null;

  const save = async (next: StatusPageSettings) => {
    setBusy(true); setNote(null);
    try { const r = await api.statusPageSave(next); setSet(r.settings); setRouted(r.hostRouted); }
    catch (e) { setNote(err(e)); }
    finally { setBusy(false); }
  };

  return (
    <Card
      title="Status page"
      description="One page, no login, for the people asking whether it is them or you. Each check decides for itself whether it appears; what a check points at is never published."
    >
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-1.5 text-sm">
          <input type="checkbox" checked={set.enabled} disabled={busy} onChange={(e) => void save({ ...set, enabled: e.target.checked })} />
          Publish a status page
        </label>
        {set.enabled && (
          <a href="/status" target="_blank" rel="noreferrer noopener" className="-my-1 py-1 text-sm text-accent underline underline-offset-2">Open it</a>
        )}
        {!open && <Button variant="secondary" className="h-8 text-xs" onClick={() => setOpen(true)}>Edit</Button>}
      </div>
      {note && <div className="mt-3"><Alert>{note}</Alert></div>}
      {set.enabled && set.host && !routed && (
        <div className="mt-3"><Alert>
          Nothing routes {set.host} yet, so a request for it gets the proxy&apos;s 404 rather than this page. Add it under Domains, pointed at the panel.
        </Alert></div>
      )}
      {open && (
        <form className="mt-3 grid grid-cols-1 gap-3 border-t border-border pt-3 sm:grid-cols-2"
          onSubmit={(e: FormEvent) => { e.preventDefault(); void save(set).then(() => setOpen(false)); }}>
          <Field label="Title" hint="Whatever the people reading it call you.">
            <Input value={set.title} onChange={(e) => setSet({ ...set, title: e.target.value })} placeholder="Acme Status" />
          </Field>
          <Field label="Hostname" hint="Optional. Naming one adds the domain for it, as the media service does.">
            <Input value={set.host} onChange={(e) => setSet({ ...set, host: e.target.value })} placeholder="status.example.com" className="font-mono" />
          </Field>
          <div className="sm:col-span-2">
            <Field label="Message" hint="A line under the heading. Maintenance tonight, where to write in — or nothing.">
              <Input value={set.message} onChange={(e) => setSet({ ...set, message: e.target.value })} />
            </Field>
          </div>
          <div className="flex gap-2 sm:col-span-2">
            <Button type="submit" disabled={busy}>Save</Button>
            <Button type="button" variant="secondary" onClick={() => { setOpen(false); void load(); }}>Cancel</Button>
          </div>
        </form>
      )}
    </Card>
  );
}
