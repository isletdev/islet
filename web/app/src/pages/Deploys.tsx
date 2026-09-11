import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, RequestError, type DeployApp, type Detection, type GitHubRepo, type Release } from "@/lib/api";
import { postStream, streamLines } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";

const SELECT = "h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm";
const STRATEGIES: Record<string, string> = { auto: "Detect automatically", static: "Static site (build, then serve files)", node: "Node service", python: "Python service", go: "Go service", dockerfile: "Your Dockerfile", compose: "Your Compose file", image: "Docker image" };
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function dur(ms: number) { return ms < 1000 ? `${ms} ms` : ms < 60000 ? `${(ms / 1000).toFixed(0)} s` : `${(ms / 60000).toFixed(1)} min`; }
function err(e: unknown) { return e instanceof RequestError ? e.message : e instanceof Error ? e.message : String(e); }
const blank = (): Partial<DeployApp> => ({ id: "", name: "", source: "git", repoUrl: "", branch: "main", rootDir: "", image: "", strategy: "auto", framework: "", installCmd: "", buildCmd: "", startCmd: "", outputDir: "", port: 0, healthPath: "/", predeployCmd: "", env: "", domain: "", tls: "letsencrypt", autoDeploy: true, memoryMb: 0, cpus: 0, volumes: "" });

export default function Deploys() {
  const { state } = useAuth();
  const role = state.status === "authed" ? state.me.user.role : "viewer";
  const canEdit = role === "admin";
  const [params, setParams] = useSearchParams();
  const [apps, setApps] = useState<DeployApp[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<DeployApp> | null>(null);
  const selected = params.get("app");
  const load = useCallback(() => api.deployApps().then((a) => { setApps(a); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); const id = setInterval(() => void load(), 10000); return () => clearInterval(id); }, [load]);
  const sel = apps.find((a) => a.id === selected);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-ink-muted">Apps built from a Git repository or an image, with releases you can roll back to.</p>
        {canEdit && <Button className="h-8 text-xs" onClick={() => setEditing(blank())}>New app</Button>}
      </div>
      {error && <Alert>{error}</Alert>}
      {editing && <AppForm initial={editing} onClose={() => setEditing(null)} onSaved={async (a) => { setEditing(null); await load(); setParams({ app: a.id }); }} />}
      {apps.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((a) => (
            <button key={a.id} type="button" onClick={() => setParams({ app: a.id })} className={`rounded-lg border p-4 text-left transition-colors ${selected === a.id ? "border-ink bg-surface-2" : "border-border bg-surface hover:bg-surface-2"}`}>
              <div className="flex items-center justify-between"><span className="font-semibold">{a.name}</span><span className={`h-2 w-2 rounded-full ${a.deploying ? "bg-accent animate-pulse" : a.status === "live" ? "bg-success" : a.status === "failed" ? "bg-danger" : "bg-ink-faint"}`} /></div>
              <div className="mt-1 text-xs text-ink-muted">{a.framework || STRATEGIES[a.strategy]} · {a.source === "git" ? a.branch : a.image}</div>
              <div className="mt-2 truncate font-mono text-[11px] text-ink-faint">{a.domain || "no domain"}</div>
            </button>
          ))}
        </div>
      )}
      {apps.length === 0 && !editing && <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-ink-muted">No apps yet. Connect a repository and Islet detects how to build and run it.</div>}
      {sel && <AppDetail app={sel} canEdit={canEdit} canDeploy={role !== "viewer"} onChanged={load} onEdit={() => setEditing({ ...sel })} />}
    </div>
  );
}

function AppDetail({ app, canEdit, canDeploy, onChanged, onEdit }: { app: DeployApp; canEdit: boolean; canDeploy: boolean; onChanged: () => Promise<void>; onEdit: () => void }) {
  const [releases, setReleases] = useState<Release[]>([]);
  const [log, setLog] = useState<string[] | null>(null);
  const [open, setOpen] = useState<Release | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [showHook, setShowHook] = useState(false);
  const box = useRef<HTMLPreElement>(null);
  const loadRel = useCallback(() => api.releases(app.id).then(setReleases).catch(() => {}), [app.id]);
  useEffect(() => { void loadRel(); setOpen(null); setLog(null); setMsg(null); }, [loadRel]);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [log]);
  // Attach to a deploy already in progress (started elsewhere or before a reload).
  useEffect(() => {
    if (!app.deploying || busy) return;
    setLog([]); setBusy(true);
    const stop = streamLines(`/api/v1/apps/${app.id}/deploy/log`, (l) => setLog((p) => [...(p ?? []), l]), async (m) => { if (m !== "done") setLog((p) => [...(p ?? []), `[islet] ${m}`]); setBusy(false); await loadRel(); await onChanged(); });
    return stop;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.id, app.deploying]);

  const run = async (query: string) => {
    setBusy(true); setLog([]); setOpen(null); setMsg(null);
    try { await postStream(`/api/v1/apps/${app.id}/deploy${query}`, (l) => setLog((p) => [...(p ?? []), l])); }
    catch (e) { setLog((p) => [...(p ?? []), `[islet] ${err(e)}`]); }
    finally { setBusy(false); await loadRel(); await onChanged(); }
  };
  const cancel = async () => { try { await api.deployCancel(app.id); } catch (e) { setMsg(err(e)); } };
  const addService = async (engine: string) => {
    if (!confirm(`Install ${engine} next to ${app.name}, create a database for it and put the connection URL in the app's environment?`)) return;
    setBusy(true); setLog([]); setOpen(null);
    try { await postStream(`/api/v1/apps/${app.id}/services`, (l) => { setLog((p) => [...(p ?? []), l]); if (l.startsWith("error:")) throw new Error(l); }, { engine }); }
    catch (e) { setLog((p) => [...(p ?? []), `[islet] ${err(e)}`]); }
    finally { setBusy(false); await onChanged(); }
  };
  const remove = async () => { if (!confirm(`Delete ${app.name}? Its containers, images, releases and route are removed. Volumes are kept.`)) return; await api.deployAppDelete(app.id); await onChanged(); };
  const show = async (r: Release) => { setLog(null); setOpen(await api.release(app.id, r.id)); };
  const hookUrl = `${location.origin}/api/v1/hooks/deploy/${app.id}`;
  const last = app.lastRelease;

  return (
    <div className="space-y-4">
      <Card title={app.name} description={`${app.framework || STRATEGIES[app.strategy]} · ${app.source === "git" ? `${app.repoUrl} @ ${app.branch}${app.rootDir ? ` /${app.rootDir}` : ""}` : app.image}`}>
        <div className="flex flex-wrap items-center gap-2">
          {app.url && <a href={app.url} target="_blank" rel="noreferrer" className="mr-2 text-sm text-accent hover:underline">{app.url}</a>}
          {canDeploy && <Button className="h-8 text-xs" disabled={busy || app.deploying} onClick={() => void run("")}>{busy || app.deploying ? "Deploying…" : app.currentRelease ? "Deploy latest" : "Deploy"}</Button>}
          {canDeploy && app.currentRelease > 0 && <Button variant="secondary" className="h-8 text-xs" disabled={busy || app.deploying} onClick={() => void run("?redeploy=1")}>Redeploy</Button>}
          {canDeploy && (busy || app.deploying) && <Button variant="danger" className="h-8 text-xs" onClick={() => void cancel()}>Cancel</Button>}
          {app.container && <Link to={`/containers?c=${app.container}`} className="text-xs text-ink-muted hover:text-ink">Logs and shell</Link>}
          {canEdit && <button type="button" onClick={onEdit} className="text-xs text-ink-muted hover:text-ink">Settings</button>}
          {canEdit && <span className="flex items-center gap-1 text-xs text-ink-muted">Add {(["postgres", "mysql", "redis"] as const).map((e) => <button key={e} type="button" disabled={busy || app.deploying} onClick={() => void addService(e)} className="rounded-sm border border-border-strong px-1.5 py-0.5 hover:text-ink">{e}</button>)}</span>}
          {canEdit && <button type="button" onClick={() => setShowHook(!showHook)} className="text-xs text-ink-muted hover:text-ink">Auto-deploy</button>}
          {canEdit && <button type="button" onClick={() => void remove()} className="ml-auto text-xs text-danger hover:underline">Delete app</button>}
        </div>
        {msg && <p className="mt-2 text-xs text-ink-muted">{msg}</p>}
        {last && !log && !open && <p className="mt-2 text-xs text-ink-muted">Last: release #{last.number} <RelStatus s={last.status} /> · {last.trigger} · {fmt(last.startedAt)}{last.durationMs > 0 && ` · ${dur(last.durationMs)}`}{last.error && <span className="text-danger"> · {last.error}</span>}</p>}
        {showHook && (
          <div className="mt-3 rounded-md border border-border p-3 text-xs">
            <p className="mb-2">Add this webhook to the repository ({app.autoDeploy ? "auto-deploy is on" : "auto-deploy is off in Settings"}). GitHub: Settings → Webhooks, content type JSON, secret below. GitLab: Settings → Webhooks with the secret token. Gitea: Settings → Webhooks (Gitea type).</p>
            <pre className="overflow-x-auto rounded-md bg-bg p-2 font-mono">{hookUrl}{"\n"}secret: {app.webhookSecret}</pre>
            <p className="mt-2 text-ink-muted">Only pushes to <span className="font-mono">{app.branch}</span> deploy. No tokens or keys are stored in the repository.</p>
          </div>
        )}
        {(log || open) && (
          <div className="mt-3">
            <div className="mb-1 flex items-center justify-between text-xs text-ink-muted"><span>{open ? <>Release #{open.number} <RelStatus s={open.status} /> · {open.trigger} by {open.actor} · {fmt(open.startedAt)}{open.commit && <> · <span className="font-mono">{open.commit.slice(0, 7)}</span> {open.message}</>}</> : "Deploy log"}</span><button type="button" onClick={() => { setLog(null); setOpen(null); }} className="hover:text-ink">Close</button></div>
            <pre ref={box} className="max-h-[28rem] overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{open ? open.log || "(no log)" : (log ?? []).join("\n") || "starting…"}</pre>
          </div>
        )}
      </Card>
      <Card title="Releases" description="Newest first. Roll back re-points to a previous image without rebuilding.">
        <table className="w-full text-sm"><tbody className="divide-y divide-border">
          {releases.map((r) => (
            <tr key={r.id} className="cursor-pointer hover:bg-surface-2" onClick={() => void show(r)}>
              <td className="py-1.5 pr-2 font-mono text-xs">#{r.number}</td>
              <td className="py-1.5 pr-2 text-xs"><RelStatus s={r.status} /></td>
              <td className="py-1.5 pr-2 text-xs"><span className="font-mono">{r.commit.slice(0, 7)}</span> <span className="text-ink-muted">{r.message.slice(0, 60)}{r.author && ` · ${r.author}`}</span></td>
              <td className="py-1.5 pr-2 text-xs text-ink-muted whitespace-nowrap">{r.trigger} · {fmt(r.startedAt)}{r.durationMs > 0 && ` · ${dur(r.durationMs)}`}</td>
              <td className="py-1.5 text-right text-xs whitespace-nowrap" onClick={(e) => e.stopPropagation()}>{canDeploy && r.image && r.status !== "live" && r.status !== "failed" && r.status !== "cancelled" && <button type="button" disabled={busy || app.deploying} onClick={() => void run(`?release=${r.id}`)} className="text-ink-muted hover:text-ink">Roll back</button>}</td>
            </tr>
          ))}
          {releases.length === 0 && <tr><td className="py-2 text-ink-muted">Nothing deployed yet.</td></tr>}
        </tbody></table>
      </Card>
    </div>
  );
}

function RelStatus({ s }: { s: string }) {
  const tone = s === "live" ? "text-success" : s === "failed" || s === "cancelled" ? "text-danger" : s === "superseded" ? "text-ink-muted" : "text-accent";
  return <span className={`font-medium ${tone}`}>{s}</span>;
}

function AppForm({ initial, onClose, onSaved }: { initial: Partial<DeployApp>; onClose: () => void; onSaved: (a: DeployApp) => Promise<void> }) {
  const [a, setA] = useState<Partial<DeployApp>>(initial);
  const [det, setDet] = useState<Detection | null>(null);
  const [detecting, setDetecting] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [advanced, setAdvanced] = useState(!!initial.id);
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  useEffect(() => { void api.githubRepos().then(setRepos).catch(() => {}); }, []);
  const set = (p: Partial<DeployApp>) => setA((c) => ({ ...c, ...p }));
  const isNew = !a.id;

  const detect = async () => {
    setDetecting(true); setMsg(null);
    try {
      const d = await api.deployInspect(a.repoUrl ?? "", a.branch ?? "main", a.rootDir ?? "");
      setDet(d);
      set({ strategy: d.strategy, framework: d.framework, installCmd: d.installCmd, buildCmd: d.buildCmd, startCmd: d.strategy === "compose" ? "" : d.startCmd, outputDir: d.outputDir, port: d.port, healthPath: d.healthPath, nodeVersion: d.nodeVersion ?? "", pythonVersion: d.pythonVersion ?? "" });
      setAdvanced(true);
    } catch (e) { setMsg(err(e)); } finally { setDetecting(false); }
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setMsg(null);
    const env = [a.env ?? "", a.nodeVersion ? `ISLET_NODE_VERSION=${a.nodeVersion}` : "", a.pythonVersion ? `ISLET_PYTHON_VERSION=${a.pythonVersion}` : ""].filter(Boolean).join("\n");
    try { await onSaved(await api.deployAppSave({ ...a, env })); } catch (er) { setMsg(err(er)); } finally { setBusy(false); }
  };
  const envShown = (a.env ?? "").split("\n").filter((l) => !l.startsWith("ISLET_")).join("\n");

  return (
    <Card title={isNew ? "New app" : `Settings for ${initial.name}`} description={isNew ? "Point Islet at a repository. It clones it, tells you what it found, and you can change anything before the first deploy." : "Changes apply on the next deploy."}>
      <form onSubmit={submit} className="grid gap-4 md:grid-cols-2">
        <Field label="Name" hint="Lowercase, becomes the container name and preview domain."><Input value={a.name ?? ""} onChange={(e) => set({ name: e.target.value })} required disabled={!isNew} placeholder="shop" /></Field>
        <Field label="Source"><select value={a.source} onChange={(e) => set({ source: e.target.value as "git" | "image", strategy: e.target.value === "image" ? "image" : "auto" })} className={SELECT} disabled={!isNew}><option value="git">Git repository</option><option value="image">Docker image</option></select></Field>
        {a.source === "git" ? (
          <>
            <Field label="Repository URL" hint={repos.length ? "Pick one of the repositories the GitHub App can see, or paste any git URL." : "Public https URL, or https://user:token@host/org/repo for private repos (stored encrypted). Configure the GitHub App in Settings to pick from a list."}>
              {repos.length > 0 && <select value="" onChange={(e) => { const r = repos.find((x) => x.url === e.target.value); if (r) set({ repoUrl: r.url, branch: r.defaultBranch, name: a.name || r.fullName.split("/")[1].toLowerCase().replace(/[^a-z0-9-]/g, "-") }); }} className={`${SELECT} mb-1`}><option value="">Pick from GitHub…</option>{repos.map((r) => <option key={r.fullName} value={r.url}>{r.fullName}{r.private ? " (private)" : ""}</option>)}</select>}
              <Input value={a.repoUrl ?? ""} onChange={(e) => set({ repoUrl: e.target.value })} className="font-mono" placeholder="https://github.com/org/repo" required />
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="Branch"><Input value={a.branch ?? "main"} onChange={(e) => set({ branch: e.target.value })} className="font-mono" /></Field>
              <Field label="Root directory" hint="For monorepos."><Input value={a.rootDir ?? ""} onChange={(e) => set({ rootDir: e.target.value })} className="font-mono" placeholder="apps/web" /></Field>
            </div>
            <div className="md:col-span-2 flex flex-wrap items-center gap-3">
              <Button type="button" variant="secondary" className="h-8 text-xs" disabled={detecting || !a.repoUrl} onClick={() => void detect()}>{detecting ? "Cloning and detecting…" : "Detect"}</Button>
              {det && <span className="text-sm">{det.summary}</span>}
              {!det && !detecting && <span className="text-xs text-ink-muted">Optional: detection also runs on the first deploy.</span>}
            </div>
          </>
        ) : (
          <>
            <Field label="Image" hint="Pulled on every deploy; digest changes create a new release."><Input value={a.image ?? ""} onChange={(e) => set({ image: e.target.value })} className="font-mono" placeholder="ghcr.io/org/app:1.2.0" required /></Field>
            <Field label="Port the container listens on"><Input type="number" value={a.port || ""} onChange={(e) => set({ port: +e.target.value })} required /></Field>
          </>
        )}
        <Field label="Domain" hint={isNew ? "Leave empty for a free preview domain on sslip.io. Own domains need an A record to this server." : "Changing it re-routes the live release."}><Input value={a.domain ?? ""} onChange={(e) => set({ domain: e.target.value })} className="font-mono" placeholder="app.example.com" /></Field>
        <Field label="Certificate"><select value={a.tls ?? "letsencrypt"} onChange={(e) => set({ tls: e.target.value })} className={SELECT}><option value="letsencrypt">Let's Encrypt</option><option value="self">Self-signed</option><option value="none">None (HTTP only)</option></select></Field>
        <div className="md:col-span-2">
          <Field label="Environment variables" hint="KEY=VALUE per line, or paste a whole .env. NEXT_PUBLIC_*, VITE_* and similar are baked in at build time; the rest are injected at runtime."><textarea value={envShown} onChange={(e) => set({ env: e.target.value })} rows={5} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder={"DATABASE_URL=postgres://…\nNEXT_PUBLIC_API=https://api.example.com"} /></Field>
        </div>
        {a.source === "git" && <div className="md:col-span-2"><button type="button" onClick={() => setAdvanced(!advanced)} className="text-xs text-ink-muted hover:text-ink">{advanced ? "Hide" : "Show"} build and run settings</button></div>}
        {advanced && a.source === "git" && (
          <>
            <Field label="Strategy"><select value={a.strategy ?? "auto"} onChange={(e) => set({ strategy: e.target.value })} className={SELECT}>{Object.entries(STRATEGIES).filter(([k]) => k !== "image").map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></Field>
            <Field label="Framework (label)"><Input value={a.framework ?? ""} onChange={(e) => set({ framework: e.target.value })} placeholder="detected on deploy" /></Field>
            {a.strategy !== "dockerfile" && a.strategy !== "compose" && (
              <>
                <Field label="Install command"><Input value={a.installCmd ?? ""} onChange={(e) => set({ installCmd: e.target.value })} className="font-mono" placeholder="npm ci" /></Field>
                <Field label="Build command"><Input value={a.buildCmd ?? ""} onChange={(e) => set({ buildCmd: e.target.value })} className="font-mono" placeholder="npm run build" /></Field>
                {a.strategy === "static" || (a.strategy === "auto" && !a.startCmd) ? <Field label="Output directory"><Input value={a.outputDir ?? ""} onChange={(e) => set({ outputDir: e.target.value })} className="font-mono" placeholder="dist" /></Field> : null}
                {a.strategy !== "static" && <Field label="Start command"><Input value={a.startCmd ?? ""} onChange={(e) => set({ startCmd: e.target.value })} className="font-mono" placeholder="npm start" /></Field>}
                <Field label={a.strategy === "python" ? "Python version" : "Node version"}><Input value={a.strategy === "python" ? (a.pythonVersion ?? "") : (a.nodeVersion ?? "")} onChange={(e) => set(a.strategy === "python" ? { pythonVersion: e.target.value } : { nodeVersion: e.target.value })} placeholder={a.strategy === "python" ? "3.12" : "22"} /></Field>
              </>
            )}
            {a.strategy === "compose" && <Field label="Web service name" hint="The Compose service that receives the domain."><Input value={a.startCmd ?? ""} onChange={(e) => set({ startCmd: e.target.value })} className="font-mono" placeholder="web" /></Field>}
            {a.strategy !== "static" && a.strategy !== "compose" && <Field label="Port" hint="What the app listens on inside the container."><Input type="number" value={a.port || ""} onChange={(e) => set({ port: +e.target.value })} placeholder="3000" /></Field>}
            <Field label="Health check path" hint="Must answer 2xx or 3xx before traffic switches."><Input value={a.healthPath ?? "/"} onChange={(e) => set({ healthPath: e.target.value })} className="font-mono" /></Field>
            <Field label="Pre-deploy command" hint="Runs once in the new image before it takes traffic (migrations)."><Input value={a.predeployCmd ?? ""} onChange={(e) => set({ predeployCmd: e.target.value })} className="font-mono" placeholder="npx prisma migrate deploy" /></Field>
            <Field label="Persistent paths" hint="Container paths kept across deploys, one per line."><textarea value={a.volumes ?? ""} onChange={(e) => set({ volumes: e.target.value })} rows={2} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder="/app/.next/cache" /></Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="Memory limit (MB)" hint="0 = unlimited"><Input type="number" value={a.memoryMb ?? 0} onChange={(e) => set({ memoryMb: +e.target.value })} /></Field>
              <Field label="CPU limit" hint="0 = unlimited"><Input type="number" step="0.5" value={a.cpus ?? 0} onChange={(e) => set({ cpus: +e.target.value })} /></Field>
            </div>
          </>
        )}
        <label className="flex items-center gap-1.5 text-sm md:col-span-2"><input type="checkbox" checked={a.autoDeploy ?? true} onChange={(e) => set({ autoDeploy: e.target.checked })} />Deploy automatically on push (webhook)</label>
        <div className="flex items-center gap-2 md:col-span-2"><Button type="submit" disabled={busy}>{isNew ? "Create app" : "Save"}</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-danger">{msg}</span>}</div>
      </form>
    </Card>
  );
}
