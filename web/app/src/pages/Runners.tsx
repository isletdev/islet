import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, RequestError, type RunnerJob, type RunnerPool } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { pollInterval } from "@/lib/poll";
import { useDialog } from "@/lib/dialogs";

const PROVIDERS: Record<string, { label: string; urlHint: string; tokenHint: string }> = {
  github: { label: "GitHub Actions", urlHint: "https://github.com/org/repo for one repository, https://github.com/org for the whole organisation.", tokenHint: "A fine-grained or classic personal access token with repo (or admin:org) scope. Stored encrypted; used only to fetch short-lived registration tokens. A GitHub App will replace this." },
  gitlab: { label: "GitLab", urlHint: "https://gitlab.com or your own instance.", tokenHint: "Runner authentication token (glrt-…) from Settings → CI/CD → Runners → New runner." },
  gitea: { label: "Gitea", urlHint: "https://gitea.example.com", tokenHint: "Registration token from Site or repository administration → Actions → Runners." },
};
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }
const blank = (): Partial<RunnerPool> => ({ id: "", provider: "github", name: "", url: "", token: "", labels: "", minIdle: 1, maxRunners: 2, dockerAccess: false, memoryMb: 0, cpus: 0, enabled: true });

export default function Runners() {
  const ask = useDialog();
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [params, setParams] = useSearchParams();
  const [pools, setPools] = useState<RunnerPool[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<RunnerPool> | null>(null);
  const selected = params.get("pool");
  const load = useCallback(() => api.runnerPools().then((p) => { setPools(p); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); const stop = pollInterval(() => void load(), 15000); return stop; }, [load]);
  const sel = pools.find((p) => p.id === selected);
  const remove = async (p: RunnerPool) => { if (!(await ask.confirm({ title: `Delete the pool ${p.name}?`, body: "Its runner containers are removed and jobs stop being picked up.", confirmLabel: "Delete pool", tone: "danger" }))) return; await api.runnerPoolDelete(p.id); if (selected === p.id) setParams({}); await load(); };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Runners</h1>
          <p className="mt-1 text-ink-muted">CI runners on this server. GitHub runners are ephemeral: one container per job, scaled from the queue.</p>
        </div>
        {isAdmin && <Button className="h-8 text-xs" onClick={() => setEditing(blank())}>New pool</Button>}
      </div>
      {error && <Alert>{error}</Alert>}
      {editing && <PoolForm initial={editing} onClose={() => setEditing(null)} onSaved={async (p) => { setEditing(null); await load(); setParams({ pool: p.id }); }} />}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {pools.map((p) => (
          <button key={p.id} type="button" onClick={() => setParams({ pool: p.id })} className={`rounded-lg border p-4 text-left transition-colors ${selected === p.id ? "border-ink bg-surface-2" : "border-border bg-surface hover:bg-surface-2"}`}>
            <div className="flex items-center justify-between"><span className="font-semibold">{p.name}</span><span className={`h-2 w-2 rounded-full ${!p.enabled ? "bg-ink-faint" : p.busy > 0 ? "bg-accent" : p.idle > 0 ? "bg-success" : "bg-warning"}`} /></div>
            <div className="mt-1 text-xs text-ink-muted">{PROVIDERS[p.provider]?.label} · {p.url.replace(/^https?:\/\//, "")}</div>
            <div className="mt-2 text-xs text-ink-muted">{p.idle} idle · {p.busy} busy · max {p.maxRunners}{p.labels && ` · ${p.labels}`}</div>
          </button>
        ))}
        {pools.length === 0 && !editing && <div className="col-span-full rounded-lg border border-dashed border-border p-8 text-center text-sm text-ink-muted">No runner pools yet. Add one and your workflows can use <span className="font-mono">runs-on: self-hosted</span>.</div>}
      </div>
      {sel && <PoolDetail pool={sel} isAdmin={isAdmin} onEdit={() => setEditing({ ...sel, token: "" })} onDelete={() => void remove(sel)} />}
    </div>
  );
}

function PoolDetail({ pool, isAdmin, onEdit, onDelete }: { pool: RunnerPool; isAdmin: boolean; onEdit: () => void; onDelete: () => void }) {
  const [jobs, setJobs] = useState<RunnerJob[]>([]);
  const [wf, setWf] = useState<string | null>(null);
  const [app, setApp] = useState("my-app");
  useEffect(() => { void api.runnerJobs(pool.id).then(setJobs).catch(() => {}); setWf(null); }, [pool.id]);
  const hookUrl = `${location.origin}/api/v1/hooks/runner/${pool.id}`;
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <Card title={pool.name} description={`${PROVIDERS[pool.provider]?.label} · ${pool.url}`}>
        <ul className="divide-y divide-border text-sm">
          {pool.runners.map((r) => <li key={r.name} className="flex items-center justify-between py-1.5"><span className="font-mono text-xs">{r.name}</span><span className="text-xs text-ink-muted">{r.state}{r.busy && " · running a job"}{r.state === "running" && <Link to={`/logs?source=container:${r.name}`} className="ml-2 hover:text-ink">logs</Link>}</span></li>)}
          {pool.runners.length === 0 && <li className="py-2 text-xs text-ink-muted">{pool.enabled ? "No runner containers yet; the first one starts within 30 seconds." : "Pool is paused."}</li>}
        </ul>
        {isAdmin && <div className="mt-3 flex gap-3 text-xs"><button type="button" onClick={onEdit} className="text-ink-muted hover:text-ink">Settings</button><button type="button" onClick={onDelete} className="text-danger hover:underline">Delete pool</button></div>}
        {pool.provider === "github" && isAdmin && (
          <div className="mt-4 rounded-md border border-border p-3 text-xs">
            <p className="mb-1 font-medium">Scale from the queue</p>
            <p className="text-ink-muted">Add a repository or organisation webhook for the <span className="font-mono">workflow_job</span> event (JSON, secret below). Queued jobs start a runner immediately instead of waiting for the idle pool.</p>
            <pre className="mt-2 overflow-x-auto rounded-md bg-bg p-2 font-mono">{hookUrl}{"\n"}secret: {pool.webhookSecret}</pre>
          </div>
        )}
        {pool.provider === "github" && (
          <div className="mt-4 text-xs">
            <div className="flex flex-wrap items-center gap-2"><span className="font-medium">Deploy workflow for</span><Input value={app} onChange={(e) => setApp(e.target.value)} className="h-7 w-40 text-xs" /><Button variant="secondary" className="h-7 text-xs" onClick={async () => setWf(await api.runnerWorkflow(pool.id, app))}>Generate deploy.yml</Button></div>
            {wf && <pre className="mt-2 max-h-72 overflow-auto rounded-md border border-border bg-bg p-2 font-mono">{wf}</pre>}
            {wf && <p className="mt-1 text-ink-muted">Save as .github/workflows/deploy.yml and add the ISLET_URL and ISLET_TOKEN repository secrets.</p>}
          </div>
        )}
      </Card>
      <Card title="Recent jobs" description="From workflow_job webhooks.">
        <ul className="divide-y divide-border text-xs">
          {jobs.map((j) => <li key={j.id} className="py-1.5"><div className="flex items-center justify-between"><a href={j.url} target="_blank" rel="noreferrer" className="font-medium hover:underline">{j.name}</a><span className={j.conclusion === "success" ? "text-success" : j.conclusion === "failure" ? "text-danger" : "text-ink-muted"}>{j.conclusion || j.status}</span></div><div className="text-ink-muted">{j.repo}{j.runner && ` · ${j.runner}`} · {fmt(j.finishedAt || j.startedAt || j.queuedAt)}</div></li>)}
          {jobs.length === 0 && <li className="py-2 text-ink-muted">No jobs seen yet.</li>}
        </ul>
      </Card>
    </div>
  );
}

function PoolForm({ initial, onClose, onSaved }: { initial: Partial<RunnerPool>; onClose: () => void; onSaved: (p: RunnerPool) => Promise<void> }) {
  const [p, setP] = useState<Partial<RunnerPool>>(initial);
  const [msg, setMsg] = useState<string | null>(null);
  const set = (x: Partial<RunnerPool>) => setP((c) => ({ ...c, ...x }));
  const pr = PROVIDERS[p.provider ?? "github"];
  const submit = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await onSaved(await api.runnerPoolSave(p)); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title={p.id ? `Settings for ${initial.name}` : "New runner pool"} description="Runners run inside Docker on this server. Jobs get a clean container each time.">
      <form onSubmit={submit} className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Field label="Name"><Input value={p.name ?? ""} onChange={(e) => set({ name: e.target.value })} required disabled={!!p.id} placeholder="main" /></Field>
        <Field label="Provider"><Select value={p.provider} onChange={(e) => set({ provider: e.target.value })} disabled={!!p.id}>{Object.entries(PROVIDERS).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}</Select></Field>
        <Field label="URL" hint={pr.urlHint}><Input value={p.url ?? ""} onChange={(e) => set({ url: e.target.value })} className="font-mono" required /></Field>
        <Field label={p.provider === "github" ? "Token (optional with the GitHub App)" : "Token"} hint={p.id ? "Leave empty to keep the stored token." : p.provider === "github" ? "Leave empty when the GitHub App in Settings is installed on this repository or organisation. Otherwise: " + pr.tokenHint : pr.tokenHint}><Input type="password" value={p.token ?? ""} onChange={(e) => set({ token: e.target.value })} autoComplete="off" /></Field>
        <Field label="Labels" hint="Comma separated, besides self-hosted."><Input value={p.labels ?? ""} onChange={(e) => set({ labels: e.target.value })} placeholder="linux, x64, islet" /></Field>
        {p.provider === "github" && <div className="grid grid-cols-2 gap-2"><Field label="Idle runners" hint="Kept ready between jobs."><Input type="number" min={0} max={10} value={p.minIdle ?? 1} onChange={(e) => set({ minIdle: +e.target.value })} /></Field><Field label="Maximum" hint="Concurrent jobs."><Input type="number" min={1} max={20} value={p.maxRunners ?? 2} onChange={(e) => set({ maxRunners: +e.target.value })} /></Field></div>}
        <div className="grid grid-cols-2 gap-2"><Field label="Memory limit (MB)" hint="0 = unlimited"><Input type="number" value={p.memoryMb ?? 0} onChange={(e) => set({ memoryMb: +e.target.value })} /></Field><Field label="CPU limit" hint="0 = unlimited"><Input type="number" step="0.5" value={p.cpus ?? 0} onChange={(e) => set({ cpus: +e.target.value })} /></Field></div>
        <label className="flex items-start gap-1.5 text-sm md:col-span-2"><input type="checkbox" checked={p.dockerAccess ?? false} onChange={(e) => set({ dockerAccess: e.target.checked })} className="mt-1" /><span>Allow jobs to use Docker (mounts the Docker socket)<span className="block text-xs text-warning">Anyone who can push to this repository can then control every container on this server. Only for repositories you trust completely.</span></span></label>
        <label className="flex items-center gap-1.5 text-sm md:col-span-2"><input type="checkbox" checked={p.enabled ?? true} onChange={(e) => set({ enabled: e.target.checked })} />Enabled</label>
        <div className="flex items-center gap-2 md:col-span-2"><Button type="submit">{p.id ? "Save" : "Create pool"}</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-danger">{msg}</span>}</div>
      </form>
    </Card>
  );
}
