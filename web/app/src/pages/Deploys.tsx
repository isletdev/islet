import React, { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, RequestError, type DeployApp, type Detection, type GitHubRepo, type Release } from "@/lib/api";
import { postStream, streamLines } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { capLines } from "@/lib/logcap";
import { pollInterval } from "@/lib/poll";
import { useDialog } from "@/lib/dialogs";

const SAMPLES = [
  { dir: "static-site", label: "Static site (HTML + CSS)" },
  { dir: "node-api", label: "Node API (no dependencies)" },
  { dir: "go-service", label: "Go service" },
  { dir: "python-fastapi", label: "Python FastAPI" },
];
const STRATEGIES: Record<string, string> = { auto: "Detect automatically", static: "Static site (build, then serve files)", node: "Node service", python: "Python service", go: "Go service", php: "PHP (nginx + PHP-FPM)", ruby: "Ruby / Rails", rust: "Rust binary", java: "Java (Maven or Gradle)", dotnet: ".NET", dockerfile: "Your Dockerfile", compose: "Your Compose file", image: "Docker image" };
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function dur(ms: number) { return ms < 1000 ? `${ms} ms` : ms < 60000 ? `${(ms / 1000).toFixed(0)} s` : `${(ms / 60000).toFixed(1)} min`; }
function err(e: unknown) { return e instanceof RequestError ? e.message : e instanceof Error ? e.message : String(e); }
const blank = (): Partial<DeployApp> => ({ id: "", name: "", source: "git", repoUrl: "", branch: "main", rootDir: "", image: "", strategy: "auto", framework: "", installCmd: "", buildCmd: "", startCmd: "", outputDir: "", port: 0, healthPath: "/", predeployCmd: "", env: "", domain: "", tls: "letsencrypt", autoDeploy: true, memoryMb: 0, cpus: 0, ioMbps: 0, volumes: "", processes: "", deployOn: "push" });

export default function Deploys() {
  const { state } = useAuth();
  const role = state.status === "authed" ? state.me.user.role : "viewer";
  const canEdit = role === "admin";
  const [params, setParams] = useSearchParams();
  const [apps, setApps] = useState<DeployApp[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<DeployApp> | null>(null);
  const [groups, setGroups] = useState(false);
  const selected = params.get("app");
  const load = useCallback(() => api.deployApps().then((a) => { setApps(a); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); const stop = pollInterval(() => void load(), 10000); return stop; }, [load]);
  const sel = apps.find((a) => a.id === selected);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-ink-muted">Apps built from a Git repository or an image, with releases you can roll back to.</p>
        {canEdit && <div className="flex gap-2"><Button variant="secondary" className="h-8 text-xs" onClick={() => setGroups(!groups)}>Env groups</Button><Button className="h-8 text-xs" onClick={() => setEditing(blank())}>New app</Button></div>}
      </div>
      {groups && <EnvGroups />}
      {error && <Alert>{error}</Alert>}
      {editing && <AppForm initial={editing} onClose={() => setEditing(null)} onSaved={async (a) => { setEditing(null); await load(); setParams({ app: a.id }); }} />}
      {apps.length > 0 && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
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
      {sel && <AppDetail app={sel} apps={apps} canEdit={canEdit} canDeploy={role !== "viewer"} onChanged={load} onEdit={() => setEditing({ ...sel })} />}
    </div>
  );
}

function AppDetail({ app, apps, canEdit, canDeploy, onChanged, onEdit }: { app: DeployApp; apps: DeployApp[]; canEdit: boolean; canDeploy: boolean; onChanged: () => Promise<void>; onEdit: () => void }) {
  const ask = useDialog();
  const targets = apps.filter((x) => x.id !== app.id && x.source === "git" && x.strategy !== "compose");
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
    const stop = streamLines(`/api/v1/apps/${app.id}/deploy/log`, (l) => setLog((p) => capLines(p, l)), async (m) => { if (m !== "done") setLog((p) => [...(p ?? []), `[islet] ${m}`]); setBusy(false); await loadRel(); await onChanged(); });
    return stop;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.id, app.deploying]);

  const run = async (query: string) => {
    setBusy(true); setLog([]); setOpen(null); setMsg(null);
    try { await postStream(`/api/v1/apps/${app.id}/deploy${query}`, (l) => setLog((p) => capLines(p, l))); }
    catch (e) { setLog((p) => [...(p ?? []), `[islet] ${err(e)}`]); }
    finally { setBusy(false); await loadRel(); await onChanged(); }
  };
  const cancel = async () => { try { await api.deployCancel(app.id); } catch (e) { setMsg(err(e)); } };
  const addService = async (engine: string) => {
    if (!(await ask.confirm({ title: `Add ${engine} to ${app.name}?`, body: "Islet installs it, creates a database, and puts the connection URL into the app's environment. The next deploy picks it up.", confirmLabel: `Add ${engine}` }))) return;
    setBusy(true); setLog([]); setOpen(null);
    try { await postStream(`/api/v1/apps/${app.id}/services`, (l) => { setLog((p) => capLines(p, l)); if (l.startsWith("error:")) throw new Error(l); }, { engine }); }
    catch (e) { setLog((p) => [...(p ?? []), `[islet] ${err(e)}`]); }
    finally { setBusy(false); await onChanged(); }
  };
  const promote = async (to: string) => {
    const t = targets.find((x) => x.id === to);
    if (!t || !(await ask.confirm({ title: `Promote ${app.name} to ${t.name}?`, body: "The image that is live now is deployed as it is, with no rebuild, so what you tested is what ships.", confirmLabel: "Promote" }))) return;
    setBusy(true); setLog([]); setOpen(null); setMsg(null);
    try { await postStream(`/api/v1/apps/${app.id}/promote`, (l) => setLog((p) => capLines(p, l)), { to }); setMsg(`Promoted to ${t.name}.`); }
    catch (e) { setLog((p) => [...(p ?? []), `[islet] ${err(e)}`]); }
    finally { setBusy(false); await onChanged(); }
  };
  const remove = async () => { if (!(await ask.confirm({ title: `Delete the app ${app.name}?`, body: "Its containers, images, release history and domain route are removed. Volumes are kept, so any data stays.", typeToConfirm: app.name, confirmLabel: "Delete app", tone: "danger" }))) return; await api.deployAppDelete(app.id); await onChanged(); };
  const show = async (r: Release) => { setLog(null); setOpen(await api.release(app.id, r.id)); };
  const hookUrl = `${location.origin}/api/v1/hooks/deploy/${app.id}`;
  const last = app.lastRelease;

  return (
    <div className="space-y-4">
      <Card title={app.name} description={`${app.framework || STRATEGIES[app.strategy]} · ${app.source === "git" ? `${app.repoUrl} @ ${app.branch}${app.rootDir ? ` /${app.rootDir}` : ""}` : app.source === "upload" ? "uploaded files" : app.image}`}>
        <div className="flex flex-wrap items-center gap-2">
          {app.url && <a href={app.url} target="_blank" rel="noreferrer" className="mr-2 text-sm text-accent hover:underline">{app.url}</a>}
          {canDeploy && <Button className="h-8 text-xs" disabled={busy || app.deploying} onClick={() => void run("")}>{busy || app.deploying ? "Deploying…" : app.currentRelease ? "Deploy latest" : "Deploy"}</Button>}
          {canDeploy && app.currentRelease > 0 && <Button variant="secondary" className="h-8 text-xs" disabled={busy || app.deploying} onClick={() => void run("?redeploy=1")}>Redeploy</Button>}
          {canDeploy && (busy || app.deploying) && <Button variant="danger" className="h-8 text-xs" onClick={() => void cancel()}>Cancel</Button>}
          {app.container && <Link to={`/containers?c=${app.container}`} className="text-xs text-ink-muted hover:text-ink">Logs and shell</Link>}
          {canEdit && <button type="button" onClick={onEdit} className="text-xs text-ink-muted hover:text-ink">Settings</button>}
          {canEdit && <span className="flex items-center gap-1 text-xs text-ink-muted">Add {(["postgres", "mysql", "redis"] as const).map((e) => <button key={e} type="button" disabled={busy || app.deploying} onClick={() => void addService(e)} className="rounded-sm border border-border-strong px-1.5 py-0.5 hover:text-ink">{e}</button>)}</span>}
          {canEdit && <button type="button" onClick={() => setShowHook(!showHook)} className="text-xs text-ink-muted hover:text-ink">Auto-deploy</button>}
          {canDeploy && app.currentRelease > 0 && app.strategy !== "compose" && targets.length > 0 && <Select value="" disabled={busy || app.deploying} onChange={(e) => { if (e.target.value) void promote(e.target.value); }} className="h-7 w-auto px-1.5 text-xs"><option value="">Promote to…</option>{targets.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</Select>}
          {canEdit && <button type="button" onClick={() => void remove()} className="ml-auto text-xs text-danger hover:underline">Delete app</button>}
        </div>
        {(app.processList?.length ?? 0) > 0 && <p className="mt-2 text-xs text-ink-muted">Processes: web{app.processList!.map((p) => <span key={p.name}> · <Link to={`/containers?c=islet-${app.name}-${p.name}-1-r${app.currentRelease}`} className="hover:text-ink">{p.name}{p.count > 1 ? ` ×${p.count}` : ""}</Link> <span className="font-mono">{p.cmd}</span></span>)}</p>}
        {app.source === "upload" && canEdit && <UploadZone appId={app.id} busy={busy || app.deploying} onUploaded={(m) => { setMsg(m); void run(""); }} onError={(m) => setMsg(m)} />}
        {msg && <p className="mt-2 text-xs text-ink-muted">{msg}</p>}
        {last && !log && !open && <p className="mt-2 text-xs text-ink-muted">Last: release #{last.number} <RelStatus s={last.status} /> · {last.trigger} · {fmt(last.startedAt)}{last.durationMs > 0 && ` · ${dur(last.durationMs)}`}{last.error && <span className="text-danger"> · {last.error}</span>}</p>}
        {showHook && (
          <div className="mt-3 rounded-md border border-border p-3 text-xs">
            <p className="mb-2">Add this webhook to the repository ({app.autoDeploy ? "auto-deploy is on" : "auto-deploy is off in Settings"}). GitHub: Settings → Webhooks, content type JSON, secret below. GitLab: Settings → Webhooks with the secret token. Gitea: Settings → Webhooks (Gitea type).</p>
            <pre className="overflow-x-auto rounded-md bg-bg p-2 font-mono">{hookUrl}{"\n"}secret: {app.webhookSecret}</pre>
            <p className="mt-2 text-ink-muted">{app.deployOn === "ci" ? <>Deploys after CI passes: with the GitHub App, a successful workflow run on <span className="font-mono">{app.branch}</span> starts it; on any CI, add a final job step <span className="font-mono">curl -X POST -H "X-Gitlab-Token: {app.webhookSecret}" {hookUrl}</span>.</> : <>Only pushes to <span className="font-mono">{app.branch}</span> deploy.</>} No tokens or keys are stored in the repository.</p>
          </div>
        )}
        {(log || open) && (
          <div className="mt-3">
            <div className="mb-1 flex items-center justify-between text-xs text-ink-muted"><span>{open ? <>Release #{open.number} <RelStatus s={open.status} /> · {open.trigger} by {open.actor} · {fmt(open.startedAt)}{open.commit && <> · <span className="font-mono">{open.commit.slice(0, 7)}</span> {open.message}</>}</> : "Deploy log"}</span><button type="button" onClick={() => { setLog(null); setOpen(null); }} className="hover:text-ink">Close</button></div>
            <pre ref={box} className="max-h-[60vh] overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-[11px] text-[#FAFAFA] whitespace-pre-wrap break-all md:max-h-[28rem] md:text-xs">{open ? open.log || "(no log)" : (log ?? []).join("\n") || "starting…"}</pre>
          </div>
        )}
      </Card>
      <Card title="Releases" description="Newest first. Roll back re-points to a previous image without rebuilding.">
        <div className="overflow-x-auto"><table className="w-full min-w-[560px] text-sm"><tbody className="divide-y divide-border">
          {releases.map((r) => (
            <tr key={r.id} className="cursor-pointer hover:bg-surface-2" onClick={() => void show(r)}>
              <td className="py-1.5 pr-2 font-mono text-xs">#{r.number}</td>
              <td className="py-1.5 pr-2 text-xs"><RelStatus s={r.status} /></td>
              <td className="py-1.5 pr-2 text-xs"><span className="font-mono">{r.commit.slice(0, 7)}</span> <span className="text-ink-muted">{r.message.slice(0, 60)}{r.author && ` · ${r.author}`}</span></td>
              <td className="py-1.5 pr-2 text-xs text-ink-muted whitespace-nowrap">{r.trigger} · {fmt(r.startedAt)}{r.durationMs > 0 && ` · ${dur(r.durationMs)}`}</td>
              <td className="py-1.5 text-right text-xs whitespace-nowrap" onClick={(e) => e.stopPropagation()}>{canDeploy && r.image && r.status !== "live" && r.status !== "failed" && r.status !== "cancelled" && <button type="button" disabled={busy || app.deploying} onClick={() => void run(`?release=${r.id}`)} className="text-ink-muted hover:text-ink">Roll back</button>}</td>
            </tr>
          ))}
          {releases.length === 0 && <tr><td className="py-2 text-ink-muted">Nothing deployed yet. Press Deploy: Islet clones, detects, builds, health-checks and routes; each attempt lands here with its log.</td></tr>}
        </tbody></table></div>
      </Card>
    </div>
  );
}

function EnvGroups() {
  const ask = useDialog();
  const [list, setList] = useState<{ name: string; keys: string[]; env?: string }[]>([]);
  const [name, setName] = useState(""); const [env, setEnv] = useState(""); const [msg, setMsg] = useState<string | null>(null);
  const load = () => api.envGroups().then(setList).catch(() => {});
  useEffect(() => { void load(); }, []);
  const save = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await api.envGroupSave(name, env); setName(""); setEnv(""); await load(); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title="Shared env groups" description="The same variables across apps (a Sentry DSN, an SMTP relay). Add a line @name to an app's environment to pull a group in; the app's own lines win on conflicts.">
      <ul className="divide-y divide-border text-sm">{list.map((g) => <li key={g.name} className="flex items-center justify-between py-1.5"><span><span className="font-mono">@{g.name}</span> <span className="text-xs text-ink-muted">{g.keys.join(", ")}</span></span><span className="flex gap-3 text-xs"><button type="button" onClick={() => { setName(g.name); setEnv(g.env ?? ""); }} className="text-ink-muted hover:text-ink">Edit</button><button type="button" onClick={async () => { if (await ask.confirm({ title: `Delete the group @${g.name}?`, body: "Apps using it lose those variables on their next deploy.", confirmLabel: "Delete group", tone: "danger" })) { await api.envGroupDelete(g.name); await load(); } }} className="text-danger hover:underline">Delete</button></span></li>)}{list.length === 0 && <li className="py-1.5 text-xs text-ink-muted">No groups yet. Good first group: @shared with SENTRY_DSN and SMTP_URL, then add the line @shared to each app.</li>}</ul>
      <form onSubmit={save} className="mt-3 grid grid-cols-1 gap-2 border-t border-border pt-3 sm:grid-cols-[200px_minmax(0,1fr)_auto]">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="group-name" className="font-mono" required />
        <textarea value={env} onChange={(e) => setEnv(e.target.value)} rows={3} className="rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder={"SENTRY_DSN=https://…\nSMTP_URL=smtp://…"} required />
        <Button type="submit" className="h-9 self-start text-xs">Save group</Button>
        {msg && <p className="text-xs text-danger sm:col-span-3">{msg}</p>}
      </form>
    </Card>
  );
}

function UploadZone({ appId, busy, onUploaded, onError }: { appId: string; busy: boolean; onUploaded: (msg: string) => void; onError: (msg: string) => void }) {
  const [over, setOver] = useState(false);
  const [state, setState] = useState<string | null>(null);
  const send = async (fd: FormData, what: string) => {
    setState(`Uploading ${what}…`);
    try {
      const r = await fetch(`/api/v1/apps/${appId}/upload`, { method: "POST", body: fd, credentials: "same-origin" });
      if (!r.ok) throw new Error(((await r.json()) as { message: string }).message);
      const j = (await r.json()) as { upload: { files: number }; detection: { summary: string } };
      setState(null);
      onUploaded(`Uploaded ${j.upload.files} files. ${j.detection.summary}`);
    } catch (e) { setState(null); onError(err(e)); }
  };
  const fromFiles = (files: FileList | File[]) => {
    const list = Array.from(files);
    if (list.length === 1 && list[0].name.toLowerCase().endsWith(".zip")) { const fd = new FormData(); fd.append("zip", list[0]); void send(fd, list[0].name); return; }
    const fd = new FormData();
    for (const f of list) fd.append("file:" + ((f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name), f);
    void send(fd, `${list.length} files`);
  };
  const onDrop = async (e: React.DragEvent) => {
    e.preventDefault(); setOver(false);
    if (busy) return;
    const items = Array.from(e.dataTransfer.items);
    const entries = items.map((it) => (it as DataTransferItem & { webkitGetAsEntry?: () => FileSystemEntry | null }).webkitGetAsEntry?.()).filter(Boolean) as FileSystemEntry[];
    if (entries.length === 0) { fromFiles(e.dataTransfer.files); return; }
    const fd = new FormData(); let n = 0;
    const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
      if (entry.isFile) {
        const file = await new Promise<File>((res, rej) => (entry as FileSystemFileEntry).file(res, rej));
        if (entries.length === 1 && file.name.toLowerCase().endsWith(".zip") && prefix === "") { fd.append("zip", file); n++; return; }
        fd.append("file:" + prefix + file.name, file); n++;
      } else if (entry.isDirectory) {
        const reader = (entry as FileSystemDirectoryEntry).createReader();
        for (;;) {
          const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej));
          if (batch.length === 0) break;
          for (const b of batch) await walk(b, prefix + entry.name + "/");
        }
      }
    };
    for (const en of entries) await walk(en, "");
    if (n === 0) { onError("Nothing to upload."); return; }
    void send(fd, `${n} files`);
  };
  return (
    <div onDragOver={(e) => { e.preventDefault(); setOver(true); }} onDragLeave={() => setOver(false)} onDrop={(e) => void onDrop(e)} className={`mt-3 rounded-md border border-dashed p-3 text-xs ${over ? "border-accent bg-accent-soft" : "border-border-strong"}`}>
      {state ?? <>Drop a folder or a .zip here to deploy it, or <label className="cursor-pointer text-accent hover:underline">choose a zip<input type="file" accept=".zip" className="hidden" disabled={busy} onChange={(e) => { if (e.target.files?.length) fromFiles(e.target.files); e.target.value = ""; }} /></label> / <label className="cursor-pointer text-accent hover:underline">choose a folder<input type="file" className="hidden" disabled={busy} {...({ webkitdirectory: "", directory: "" } as Record<string, string>)} multiple onChange={(e) => { if (e.target.files?.length) fromFiles(e.target.files); e.target.value = ""; }} /></label>.</>}
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
      <form onSubmit={submit} className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Field label="Name" hint="Lowercase, becomes the container name and preview domain."><Input value={a.name ?? ""} onChange={(e) => set({ name: e.target.value })} required disabled={!isNew} placeholder="shop" /></Field>
        <Field label="Source"><Select value={a.source} onChange={(e) => set({ source: e.target.value as "git" | "image" | "upload", strategy: e.target.value === "image" ? "image" : "auto" })} disabled={!isNew}><option value="git">Git repository</option><option value="image">Docker image</option><option value="upload">Upload a folder or zip</option></Select></Field>
        {a.source === "upload" ? (
          <p className="text-sm text-ink-muted md:col-span-2">Create the app, then drop a folder or a .zip on its card. Islet detects the framework from the upload and every new upload becomes a release you can roll back.</p>
        ) : a.source === "git" ? (
          <>
            {isNew && <Field label="Try a sample" hint="Small apps from the Islet repository, one per framework."><Select value="" onChange={(e) => { const s = SAMPLES.find((x) => x.dir === e.target.value); if (s) set({ repoUrl: "https://github.com/isletdev/islet", branch: "main", rootDir: `examples/${s.dir}`, name: a.name || `sample-${s.dir}`, env: "GREETING=hello from islet" }); }}><option value="">Pick a sample…</option>{SAMPLES.map((s) => <option key={s.dir} value={s.dir}>{s.label}</option>)}</Select></Field>}
            <Field label="Repository URL" hint={repos.length ? "Pick one of the repositories the GitHub App can see, or paste any git URL." : "Public https URL, or https://user:token@host/org/repo for private repos (stored encrypted). Configure the GitHub App in Settings to pick from a list."}>
              {repos.length > 0 && <Select value="" onChange={(e) => { const r = repos.find((x) => x.url === e.target.value); if (r) set({ repoUrl: r.url, branch: r.defaultBranch, name: a.name || r.fullName.split("/")[1].toLowerCase().replace(/[^a-z0-9-]/g, "-") }); }} className="mb-1"><option value="">Pick from GitHub…</option>{repos.map((r) => <option key={r.fullName} value={r.url}>{r.fullName}{r.private ? " (private)" : ""}</option>)}</Select>}
              <Input value={a.repoUrl ?? ""} onChange={(e) => set({ repoUrl: e.target.value })} className="font-mono" placeholder="https://github.com/org/repo" required />
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="Branch or tag rule" hint="A branch name, or tag:v* to deploy tags matching a pattern."><Input value={a.branch ?? "main"} onChange={(e) => set({ branch: e.target.value })} className="font-mono" /></Field>
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
        <Field label="Domains" hint={isNew ? "Comma separated; the first is the primary. Leave empty for a free preview domain on sslip.io. Own domains need an A record to this server." : "Comma separated; the first is the primary. Changes re-route the live release."}><Input value={a.domain ?? ""} onChange={(e) => set({ domain: e.target.value })} className="font-mono" placeholder="app.example.com, www.app.example.com" /></Field>
        <Field label="Certificate"><Select value={a.tls ?? "letsencrypt"} onChange={(e) => set({ tls: e.target.value })}><option value="letsencrypt">Let's Encrypt</option><option value="self">Self-signed</option><option value="none">None (HTTP only)</option></Select></Field>
        <div className="md:col-span-2">
          <Field label="Environment variables" hint="KEY=VALUE per line, or paste a whole .env. A line @group pulls in a shared env group. NEXT_PUBLIC_*, VITE_* and similar are baked in at build time; the rest are injected at runtime."><textarea value={envShown} onChange={(e) => set({ env: e.target.value })} rows={5} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder={"DATABASE_URL=postgres://…\nNEXT_PUBLIC_API=https://api.example.com"} /></Field>
        </div>
        {a.source === "git" && <div className="md:col-span-2"><button type="button" onClick={() => setAdvanced(!advanced)} className="text-xs text-ink-muted hover:text-ink">{advanced ? "Hide" : "Show"} build and run settings</button></div>}
        {advanced && a.source === "git" && (
          <>
            <Field label="Strategy"><Select value={a.strategy ?? "auto"} onChange={(e) => set({ strategy: e.target.value })}>{Object.entries(STRATEGIES).filter(([k]) => k !== "image").map(([k, v]) => <option key={k} value={k}>{v}</option>)}</Select></Field>
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
            {a.strategy !== "static" && a.strategy !== "compose" && <Field label="Extra processes" hint="Started from the same image, one per line: name: command, or name x2: command for two instances."><textarea value={a.processes ?? ""} onChange={(e) => set({ processes: e.target.value })} rows={2} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder={"worker: node worker.js\nscheduler: node scheduler.js"} /></Field>}
            <div className="grid grid-cols-2 gap-2">
              <Field label="Memory limit (MB)" hint="0 = unlimited"><Input type="number" value={a.memoryMb ?? 0} onChange={(e) => set({ memoryMb: +e.target.value })} /></Field>
              <Field label="CPU limit" hint="0 = unlimited"><Input type="number" step="0.5" value={a.cpus ?? 0} onChange={(e) => set({ cpus: +e.target.value })} /></Field>
              <Field label="Disk IO limit (MB/s)" hint="0 = unlimited; keeps one app from starving the database of disk time."><Input type="number" value={a.ioMbps ?? 0} onChange={(e) => set({ ioMbps: +e.target.value })} /></Field>
            </div>
          </>
        )}
        <div className="flex flex-wrap items-center gap-4 md:col-span-2">
          <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={a.autoDeploy ?? true} onChange={(e) => set({ autoDeploy: e.target.checked })} />Deploy automatically</label>
          {a.source === "git" && (a.autoDeploy ?? true) && <Select value={a.deployOn ?? "push"} onChange={(e) => set({ deployOn: e.target.value as "push" | "ci" })}><option value="push">on every push</option><option value="ci">after CI passes</option></Select>}
        </div>
        <div className="flex items-center gap-2 md:col-span-2"><Button type="submit" disabled={busy}>{isNew ? "Create app" : "Save"}</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-danger">{msg}</span>}</div>
      </form>
    </Card>
  );
}
