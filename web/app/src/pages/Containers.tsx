import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, NavLink, Route, Routes, useParams } from "react-router-dom";
import { api, RequestError, type Container, type ContainerDetail, type DockerImage, type DockerNetwork, type DockerStatus, type DockerVolume, type Stack, type Registry } from "@/lib/api";
import { postStream, streamLines } from "@/lib/stream";
import { Alert, Button, Card, Field, Input } from "@/components/ui";
import TermView from "@/components/TermView";
import { bytes } from "@/lib/format";

const TABS = [
  { to: "/containers", label: "Containers", end: true },
  { to: "/containers/stacks", label: "Stacks" },
  { to: "/containers/images", label: "Images" },
  { to: "/containers/volumes", label: "Volumes" },
  { to: "/containers/networks", label: "Networks" },
];

export default function ContainersRoot() {
  const [status, setStatus] = useState<DockerStatus | null>(null);
  useEffect(() => { api.dockerStatus().then(setStatus).catch(() => setStatus({ available: false, version: "", composeVersion: "", error: "unreachable" })); }, []);

  return (
    <Routes>
      <Route path=":id" element={<Detail />} />
      <Route path="*" element={
        <div className="mx-auto max-w-6xl">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div>
              <h1 className="text-xl font-semibold tracking-[-0.02em]">Containers</h1>
              <p className="mt-1 text-ink-muted">{status?.available ? `Docker ${status.version}${status.composeVersion ? `, Compose ${status.composeVersion}` : ""}` : "Everything Docker runs on this server."}</p>
            </div>
            <nav className="flex rounded-md border border-border-strong p-0.5 text-xs font-medium">
              {TABS.map((t) => (
                <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => `rounded-sm px-2.5 py-1 ${isActive ? "bg-ink text-on-ink" : "text-ink-muted hover:text-ink"}`}>{t.label}</NavLink>
              ))}
            </nav>
          </div>
          {status && !status.available && (
            <div className="mt-4"><Alert>Docker is not reachable: {status.error}. Install it or start the daemon, then reload.</Alert></div>
          )}
          <div className="mt-6">
            <Routes>
              <Route index element={<List />} />
              <Route path="stacks" element={<Stacks />} />
              <Route path="images" element={<Images />} />
              <Route path="volumes" element={<Volumes />} />
              <Route path="networks" element={<Networks />} />
            </Routes>
          </div>
        </div>
      } />
    </Routes>
  );
}

function StateDot({ state }: { state: string }) {
  const c = state === "running" ? "bg-success" : state === "paused" || state === "restarting" ? "bg-warning" : "bg-ink-faint";
  return <span className={`inline-block h-2 w-2 rounded-full ${c}`} aria-hidden="true" />;
}

function useList<T>(load: () => Promise<T[]>, every = 5000) {
  const [rows, setRows] = useState<T[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const refresh = useCallback(() => load().then((r) => { setRows(r); setErr(null); }).catch((e) => setErr(e instanceof RequestError ? e.message : String(e))), [load]);
  useEffect(() => { void refresh(); const id = setInterval(() => void refresh(), every); return () => clearInterval(id); }, [refresh, every]);
  return { rows, err, refresh };
}

function List() {
  const { rows, err, refresh } = useList<Container>(api.containers);
  const [busy, setBusy] = useState<string | null>(null);
  const act = async (id: string, action: string) => {
    if (action === "remove" && !confirm("Remove this container? Its writable layer is lost; named volumes stay.")) return;
    setBusy(id + action);
    try { await api.containerAction(id, action); await refresh(); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); }
    finally { setBusy(null); }
  };
  return (
    <div className="overflow-x-auto rounded-lg border border-border bg-surface">
      {err && <div className="p-4"><Alert>{err}</Alert></div>}
      <table className="w-full min-w-[900px] text-sm">
        <thead className="text-left text-xs text-ink-muted"><tr>
          <th className="px-4 py-2.5 font-medium">Name</th><th className="py-2.5 font-medium">Image</th><th className="py-2.5 font-medium">Stack</th>
          <th className="py-2.5 pl-3 text-right font-medium">CPU</th><th className="py-2.5 pl-3 text-right font-medium">Memory</th><th className="py-2.5 pl-3 font-medium">Ports</th><th className="py-2.5 pr-4 text-right font-medium"></th>
        </tr></thead>
        <tbody className="divide-y divide-border">
          {rows.map((c) => (
            <tr key={c.id} className="hover:bg-surface-2">
              <td className="px-4 py-2"><Link to={`/containers/${c.id}`} className="flex items-center gap-2 font-medium text-ink hover:underline"><StateDot state={c.state} />{c.name}</Link><div className="pl-4 text-xs text-ink-muted">{c.status}</div></td>
              <td className="py-2 font-mono text-xs text-ink-muted">{c.image}</td>
              <td className="py-2 text-ink-muted">{c.stack ? <Link to="/containers/stacks" className="hover:underline">{c.stack}</Link> : ""}</td>
              <td className="whitespace-nowrap py-2 pl-3 text-right font-mono tabular-nums">{c.state === "running" ? `${c.cpuPct.toFixed(1)}%` : ""}</td>
              <td className="whitespace-nowrap py-2 pl-3 text-right font-mono tabular-nums text-xs">{c.state === "running" ? c.memUsage.split(" / ")[0] : ""}</td>
              <td className="max-w-[22ch] truncate py-2 pl-3 font-mono text-xs text-ink-muted" title={c.ports}>{c.ports}</td>
              <td className="py-2 pr-4 text-right whitespace-nowrap">
                {c.state === "running"
                  ? <><Act onClick={() => act(c.id, "restart")} busy={busy === c.id + "restart"}>Restart</Act><Act onClick={() => act(c.id, "stop")} busy={busy === c.id + "stop"}>Stop</Act></>
                  : <Act onClick={() => act(c.id, "start")} busy={busy === c.id + "start"}>Start</Act>}
                <Act onClick={() => act(c.id, "remove")} busy={busy === c.id + "remove"} danger>Remove</Act>
              </td>
            </tr>
          ))}
          {rows.length === 0 && !err && <tr><td colSpan={7} className="px-4 py-6 text-center text-ink-muted">No containers yet. Create a stack, or install an app from the catalog when it lands.</td></tr>}
        </tbody>
      </table>
    </div>
  );
}

function Act({ children, onClick, busy, danger }: { children: string; onClick: () => void; busy?: boolean; danger?: boolean }) {
  return <button type="button" onClick={onClick} disabled={busy} className={`ml-1 rounded-sm border px-2 py-0.5 text-xs font-medium disabled:opacity-50 ${danger ? "border-danger/50 text-danger hover:bg-danger-soft" : "border-border-strong text-ink hover:bg-surface-2"}`}>{busy ? "…" : children}</button>;
}

// ---- detail ----

function Detail() {
  const { id = "" } = useParams();
  const [c, setC] = useState<ContainerDetail | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [tab, setTab] = useState<"logs" | "shell" | "details" | "limits">("logs");
  const load = useCallback(() => api.container(id).then((d) => { setC(d); setErr(null); }).catch((e) => setErr(e instanceof RequestError ? e.message : String(e))), [id]);
  useEffect(() => { void load(); }, [load]);
  const act = async (action: string) => {
    try { await api.containerAction(id, action); await load(); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); }
  };
  return (
    <div className="mx-auto flex h-[calc(100vh-6rem)] max-w-6xl flex-col">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <Link to="/containers" className="text-xs text-ink-muted hover:text-ink">← Containers</Link>
          <h1 className="mt-1 flex items-center gap-2 text-xl font-semibold tracking-[-0.02em]">{c && <StateDot state={c.state} />}{c?.name ?? id}</h1>
          <p className="mt-0.5 font-mono text-xs text-ink-muted">{c?.image} {c?.stack && `· stack ${c.stack}`} {c && `· restarts ${c.restartCount}`}</p>
        </div>
        {c && (
          <div className="flex gap-1">
            {c.state === "running" ? <><Act onClick={() => act("restart")}>Restart</Act><Act onClick={() => act("stop")}>Stop</Act></> : <Act onClick={() => act("start")}>Start</Act>}
            <Act onClick={() => { if (confirm("Remove this container?")) void act("remove"); }} danger>Remove</Act>
          </div>
        )}
      </div>
      {err && <div className="mt-3"><Alert>{err}</Alert></div>}
      <div className="mt-4 flex gap-1 border-b border-border text-sm">
        {(["logs", "shell", "details", "limits"] as const).map((t) => (
          <button key={t} type="button" onClick={() => setTab(t)} className={`-mb-px border-b-2 px-3 py-1.5 font-medium ${tab === t ? "border-ink text-ink" : "border-transparent text-ink-muted hover:text-ink"}`}>{t[0].toUpperCase() + t.slice(1)}</button>
        ))}
      </div>
      <div className="mt-4 min-h-0 flex-1">
        {tab === "logs" && <Logs id={id} />}
        {tab === "shell" && (c?.state === "running" ? <TermView path={`/api/v1/docker/containers/${id}/exec`} className="h-full" /> : <p className="text-ink-muted">Start the container to open a shell.</p>)}
        {tab === "details" && c && <Details c={c} />}
        {tab === "limits" && c && <Limits c={c} onSaved={load} />}
      </div>
    </div>
  );
}

function Logs({ id }: { id: string }) {
  const [lines, setLines] = useState<string[]>([]);
  const [follow, setFollow] = useState(true);
  const [q, setQ] = useState("");
  useEffect(() => {
    setLines([]);
    return streamLines(`/api/v1/docker/containers/${id}/logs?tail=300&follow=${follow ? 1 : 0}`, (l) => setLines((ls) => [...ls.slice(-4999), l]));
  }, [id, follow]);
  const shown = q ? lines.filter((l) => l.toLowerCase().includes(q.toLowerCase())) : lines;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="mb-2 flex items-center gap-2">
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter lines" className="h-8 max-w-xs text-xs" />
        <label className="flex items-center gap-1.5 text-xs text-ink-muted"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow</label>
        <span className="ml-auto text-xs text-ink-muted">{shown.length} lines</span>
      </div>
      <pre className="min-h-0 flex-1 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs leading-relaxed text-code-fg">{shown.join("\n") || "No output yet."}</pre>
    </div>
  );
}

function Details({ c }: { c: ContainerDetail }) {
  const [showEnv, setShowEnv] = useState(false);
  return (
    <div className="grid gap-4 overflow-auto md:grid-cols-2">
      <Card title="Runtime">
        <dl className="space-y-1.5 text-sm">
          <Row k="State" v={c.state} /><Row k="Started" v={c.startedAt ? new Date(c.startedAt).toLocaleString() : "–"} /><Row k="Restart policy" v={c.restartPolicy || "no"} />
          <Row k="Command" v={c.cmd?.join(" ") || "–"} mono /><Row k="Networks" v={c.networks?.join(", ") || "–"} />
          <Row k="Memory limit" v={c.memoryLimit ? bytes(c.memoryLimit) : "unlimited"} /><Row k="CPU limit" v={c.cpuLimit ? `${c.cpuLimit} CPUs` : "unlimited"} />
        </dl>
      </Card>
      <Card title="Ports">
        <dl className="space-y-1.5 text-sm">{Object.entries(c.ports).map(([k, v]) => <Row key={k} k={k} v={v || "not published"} mono />)}{Object.keys(c.ports).length === 0 && <p className="text-ink-muted">No ports exposed.</p>}</dl>
      </Card>
      <Card title="Mounts">
        <ul className="space-y-1.5 font-mono text-xs">{c.mounts?.map((m, i) => <li key={i}><span className="text-ink-muted">{m.type}</span> {m.source} → {m.destination}{!m.rw && <span className="text-ink-muted"> (ro)</span>}</li>)}{(!c.mounts || c.mounts.length === 0) && <li className="font-sans text-sm text-ink-muted">No mounts.</li>}</ul>
      </Card>
      <Card title="Environment">
        <button type="button" onClick={() => setShowEnv((v) => !v)} className="mb-2 text-xs text-accent hover:underline">{showEnv ? "Hide values" : "Show values"}</button>
        <ul className="space-y-1 font-mono text-xs">{c.env?.map((e, i) => { const [k, ...rest] = e.split("="); return <li key={i}><span className="text-ink">{k}</span>=<span className="text-ink-muted">{showEnv ? rest.join("=") : "••••••"}</span></li>; })}</ul>
      </Card>
    </div>
  );
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return <div className="grid grid-cols-[9rem_1fr] gap-2"><dt className="text-ink-muted">{k}</dt><dd className={`break-all ${mono ? "font-mono text-xs" : ""}`}>{v}</dd></div>;
}

function Limits({ c, onSaved }: { c: ContainerDetail; onSaved: () => Promise<void> }) {
  const [mem, setMem] = useState(c.memoryLimit ? String(Math.round(c.memoryLimit / 1048576)) : "0");
  const [cpus, setCpus] = useState(String(c.cpuLimit || 0));
  const [restart, setRestart] = useState(c.restartPolicy || "no");
  const [msg, setMsg] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setMsg(null);
    try { await api.containerLimits(c.id, { memoryBytes: Number(mem) * 1048576, cpus: Number(cpus), restart }); await onSaved(); setMsg("Saved. Limits apply immediately."); }
    catch (err) { setMsg(err instanceof RequestError ? err.message : "Could not save."); }
  };
  return (
    <Card title="Resource limits" description="0 means unlimited. Memory is in MB.">
      <form onSubmit={submit} className="grid max-w-xl gap-4 md:grid-cols-3">
        <Field label="Memory (MB)"><Input value={mem} onChange={(e) => setMem(e.target.value)} inputMode="numeric" /></Field>
        <Field label="CPUs"><Input value={cpus} onChange={(e) => setCpus(e.target.value)} inputMode="decimal" /></Field>
        <Field label="Restart policy">
          <select value={restart} onChange={(e) => setRestart(e.target.value)} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm">
            {["no", "always", "unless-stopped", "on-failure"].map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        </Field>
        <div className="md:col-span-3 flex items-center gap-3"><Button type="submit">Apply</Button>{msg && <span className="text-sm text-ink-muted">{msg}</span>}</div>
      </form>
    </Card>
  );
}

// ---- stacks ----

const EXAMPLE = `services:
  web:
    image: traefik/whoami
    restart: unless-stopped
    ports:
      - "8080:80"
`;

function Stacks() {
  const { rows, err, refresh } = useList<Stack>(api.stacks, 8000);
  const [editing, setEditing] = useState<{ name: string; compose: string; env: string; isNew: boolean } | null>(null);
  const [out, setOut] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const run = async (name: string, action: string) => {
    setBusy(true); setOut([]); setMsg(null);
    try { await postStream(`/api/v1/docker/stacks/${name}/${action}`, (l) => setOut((o) => [...(o ?? []), l])); setMsg(`${action} finished.`); }
    catch (e) { setMsg(String(e instanceof Error ? e.message : e)); }
    finally { setBusy(false); await refresh(); }
  };
  const open = async (name: string) => {
    try { const s = await api.stack(name); setEditing({ ...s, isNew: false }); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); }
  };
  const save = async (e: FormEvent) => {
    e.preventDefault(); if (!editing) return;
    setBusy(true); setMsg(null);
    try { await api.stackWrite(editing.name, editing.compose, editing.env, editing.isNew); setEditing((ed) => ed && { ...ed, isNew: false }); setMsg("Saved and validated."); await refresh(); }
    catch (err) { setMsg(err instanceof RequestError ? err.message : "Could not save."); }
    finally { setBusy(false); }
  };
  const remove = async (name: string) => {
    if (!confirm(`Remove stack ${name}? Containers are stopped and removed. Volumes are kept.`)) return;
    try { await api.stackRemove(name, false); await refresh(); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); }
  };

  return (
    <div className="space-y-4">
      {err && <Alert>{err}</Alert>}
      <div className="rounded-lg border border-border bg-surface">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <span className="font-semibold">Compose stacks</span>
          <Button className="h-8 text-xs" onClick={() => { setEditing({ name: "", compose: EXAMPLE, env: "", isNew: true }); setMsg(null); }}>New stack</Button>
        </div>
        <ul className="divide-y divide-border">
          {rows.map((s) => (
            <li key={s.name} className="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5 text-sm">
              <div><span className="font-medium">{s.name}</span> <span className="ml-2 font-mono text-xs text-ink-muted">{s.status}</span>{!s.managed && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[11px] text-ink-muted">not managed by Islet</span>}{!s.managed && s.path && <button type="button" onClick={async () => { if (!confirm(`Adopt ${s.name}? Its Compose file is copied into Islet so you can edit and update it here.`)) return; try { const r = await api.stackImport(s.name); alert(r.note); await refresh(); } catch (e) { alert(e instanceof Error ? e.message : String(e)); } }} className="ml-2 text-[11px] text-accent hover:underline">Adopt</button>}</div>
              {s.managed && (
                <div className="flex gap-1">
                  <Act onClick={() => open(s.name)}>Edit</Act>
                  <Act onClick={() => run(s.name, "up")} busy={busy}>Up</Act>
                  <Act onClick={() => run(s.name, "update")} busy={busy}>Pull & update</Act>
                  <Act onClick={() => run(s.name, "down")} busy={busy}>Down</Act>
                  <Act onClick={() => remove(s.name)} danger>Remove</Act>
                </div>
              )}
            </li>
          ))}
          {rows.length === 0 && <li className="px-4 py-6 text-center text-ink-muted">No stacks. Paste any docker-compose.yml to create one.</li>}
        </ul>
      </div>
      {editing && (
        <Card title={editing.isNew ? "New stack" : `Edit ${editing.name}`} description="Saved files live under the Islet data directory and are validated with docker compose config before use.">
          <form onSubmit={save} className="space-y-3">
            {editing.isNew && <Field label="Name" hint="Lowercase letters, digits, dash or underscore."><Input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} required className="max-w-xs" /></Field>}
            <Field label="compose.yaml"><textarea value={editing.compose} onChange={(e) => setEditing({ ...editing, compose: e.target.value })} rows={14} spellCheck={false} className="w-full rounded-md border border-border-strong bg-code-bg p-3 font-mono text-xs text-code-fg" /></Field>
            <Field label=".env (optional)" hint="KEY=value lines, available as ${KEY} in the compose file."><textarea value={editing.env} onChange={(e) => setEditing({ ...editing, env: e.target.value })} rows={3} spellCheck={false} className="w-full rounded-md border border-border-strong bg-code-bg p-3 font-mono text-xs text-code-fg" /></Field>
            <div className="flex items-center gap-2"><Button type="submit" disabled={busy}>Save</Button>{!editing.isNew && <Button type="button" variant="secondary" disabled={busy} onClick={() => run(editing.name, "up")}>Save & up</Button>}<Button type="button" variant="secondary" onClick={() => setEditing(null)}>Close</Button>{msg && <span className="text-sm text-ink-muted">{msg}</span>}</div>
          </form>
        </Card>
      )}
      {out && <pre className="max-h-64 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{out.join("\n") || "…"}</pre>}
      {msg && !editing && <p className="text-sm text-ink-muted">{msg}</p>}
    </div>
  );
}

// ---- images, volumes, networks ----

function Registries() {
  const [list, setList] = useState<Registry[]>([]);
  const [f, setF] = useState({ host: "ghcr.io", username: "", password: "" });
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => api.registries().then(setList).catch(() => {});
  useEffect(() => { void load(); }, []);
  const login = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await api.registryLogin(f); setF({ ...f, password: "" }); setMsg(`Logged in to ${f.host}.`); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  return (
    <Card title="Registry logins" description="For private images on Docker Hub, GHCR, GitLab or your own registry. Applied with docker login, so pulls and deploys use them automatically.">
      <ul className="divide-y divide-border text-sm">{list.map((r) => <li key={r.host} className="flex items-center justify-between py-1.5"><span><span className="font-mono">{r.host}</span> <span className="text-xs text-ink-muted">as {r.username}</span></span><button type="button" onClick={async () => { await api.registryLogout(r.host); await load(); }} className="text-xs text-danger hover:underline">Log out</button></li>)}{list.length === 0 && <li className="py-1.5 text-xs text-ink-muted">No logins yet.</li>}</ul>
      <form onSubmit={login} className="mt-3 flex flex-wrap items-end gap-2 border-t border-border pt-3">
        <Input value={f.host} onChange={(e) => setF({ ...f, host: e.target.value })} className="w-44 font-mono" placeholder="ghcr.io" />
        <Input value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} className="w-40" placeholder="username" required autoComplete="off" />
        <Input type="password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} className="w-56" placeholder="password or token" required autoComplete="off" />
        <Button type="submit" className="h-9 text-xs">Log in</Button>
        {msg && <span className="text-xs text-ink-muted">{msg}</span>}
      </form>
    </Card>
  );
}

function Images() {
  const { rows, err, refresh } = useList<DockerImage>(api.images, 15000);
  const [ref, setRef] = useState("");
  const [out, setOut] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const pull = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setOut([]);
    try { await postStream(`/api/v1/docker/images/pull?ref=${encodeURIComponent(ref)}`, (l) => setOut((o) => [...(o ?? []).slice(-200), l])); await refresh(); }
    catch (er) { setOut((o) => [...(o ?? []), String(er instanceof Error ? er.message : er)]); }
    finally { setBusy(false); }
  };
  const remove = async (id: string) => { try { await api.imageRemove(id); await refresh(); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); } };
  return (
    <div className="space-y-4">
      {err && <Alert>{err}</Alert>}
      <form onSubmit={pull} className="flex gap-2"><Input value={ref} onChange={(e) => setRef(e.target.value)} placeholder="ghcr.io/org/image:tag" className="max-w-md" required /><Button type="submit" disabled={busy}>Pull</Button></form>
      {out && <pre className="max-h-48 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{out.join("\n") || "…"}</pre>}
      <Registries />
      <div className="rounded-lg border border-border bg-surface">
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-ink-muted"><tr><th className="px-4 py-2.5 font-medium">Image</th><th className="py-2.5 font-medium">Tag</th><th className="py-2.5 font-medium">Size</th><th className="py-2.5 font-medium">Created</th><th className="py-2.5 pr-4 text-right"></th></tr></thead>
          <tbody className="divide-y divide-border">
            {rows.map((i) => (
              <tr key={i.id + i.tag}>
                <td className="px-4 py-2 font-mono text-xs">{i.repository}{i.dangling && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">dangling</span>}{!i.inUse && !i.dangling && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">unused</span>}</td>
                <td className="py-2 font-mono text-xs">{i.tag}</td><td className="py-2 font-mono text-xs tabular-nums">{i.size}</td><td className="py-2 text-xs text-ink-muted">{i.createdAt}</td>
                <td className="py-2 pr-4 text-right"><Act onClick={() => remove(i.id)} danger>Remove</Act></td>
              </tr>
            ))}
            {rows.length === 0 && <tr><td colSpan={5} className="px-4 py-6 text-center text-ink-muted">No images.</td></tr>}
          </tbody>
        </table>
      </div>
      <Prune onDone={refresh} />
    </div>
  );
}

function Prune({ onDone }: { onDone: () => Promise<void> }) {
  const [df, setDf] = useState<{ type: string; total: number; active: number; size: string; reclaimable: string }[]>([]);
  const [sel, setSel] = useState({ containers: true, images: true, allImages: false, volumes: false, networks: true, builder: true });
  const [res, setRes] = useState<string | null>(null);
  const load = () => api.dockerDF().then(setDf).catch(() => {});
  useEffect(() => { void load(); }, []);
  const prune = async () => {
    if (sel.volumes && !confirm("Remove ALL unused volumes? Data in them is deleted permanently.")) return;
    try { const r = await api.dockerPrune(sel); setRes(Object.entries(r).map(([k, v]) => `${k}: ${v}`).join(", ") || "nothing to remove"); await load(); await onDone(); }
    catch (e) { setRes(e instanceof RequestError ? e.message : String(e)); }
  };
  return (
    <Card title="Disk usage" description="What Docker holds on disk and what can be reclaimed safely.">
      <table className="mb-3 w-full text-sm"><tbody className="divide-y divide-border">{df.map((d) => <tr key={d.type}><td className="py-1">{d.type}</td><td className="py-1 text-ink-muted">{d.total} total, {d.active} active</td><td className="py-1 text-right font-mono text-xs tabular-nums">{d.size}</td><td className="py-1 text-right font-mono text-xs tabular-nums text-ink-muted">{d.reclaimable} reclaimable</td></tr>)}</tbody></table>
      <div className="flex flex-wrap gap-3 text-sm">
        {([["containers", "stopped containers"], ["images", "dangling images"], ["allImages", "all unused images"], ["networks", "unused networks"], ["builder", "build cache"], ["volumes", "unused volumes (data loss)"]] as const).map(([k, l]) => (
          <label key={k} className="flex items-center gap-1.5"><input type="checkbox" checked={sel[k]} onChange={(e) => setSel({ ...sel, [k]: e.target.checked })} />{l}</label>
        ))}
      </div>
      <div className="mt-3 flex items-center gap-3"><Button variant={sel.volumes ? "danger" : "primary"} onClick={() => void prune()}>Remove selected</Button>{res && <span className="text-sm text-ink-muted">{res}</span>}</div>
    </Card>
  );
}

function Volumes() {
  const { rows, err, refresh } = useList<DockerVolume>(api.volumes, 15000);
  const remove = async (n: string) => { if (!confirm(`Delete volume ${n}? Its data is gone for good.`)) return; try { await api.volumeRemove(n); await refresh(); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); } };
  return (
    <div className="rounded-lg border border-border bg-surface">
      {err && <div className="p-4"><Alert>{err}</Alert></div>}
      <table className="w-full text-sm">
        <thead className="text-left text-xs text-ink-muted"><tr><th className="px-4 py-2.5 font-medium">Name</th><th className="py-2.5 font-medium">Stack</th><th className="py-2.5 font-medium">Size</th><th className="py-2.5 font-medium">Mountpoint</th><th className="py-2.5 pr-4 text-right"></th></tr></thead>
        <tbody className="divide-y divide-border">
          {rows.map((v) => (
            <tr key={v.name}><td className="px-4 py-2 font-mono text-xs">{v.name}{!v.inUse && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">unused</span>}</td><td className="py-2 text-ink-muted">{v.stack}</td><td className="py-2 font-mono text-xs tabular-nums">{v.size}</td><td className="max-w-[30ch] truncate py-2 font-mono text-xs text-ink-muted">{v.mountpoint.startsWith("/") ? <Link to={`/files?path=${encodeURIComponent(v.mountpoint)}`} className="hover:text-ink hover:underline" title="Browse in Files">{v.mountpoint}</Link> : v.mountpoint}</td><td className="py-2 pr-4 text-right">{!v.inUse && <Act onClick={() => remove(v.name)} danger>Delete</Act>}</td></tr>
          ))}
          {rows.length === 0 && <tr><td colSpan={5} className="px-4 py-6 text-center text-ink-muted">No volumes.</td></tr>}
        </tbody>
      </table>
    </div>
  );
}

function Networks() {
  const { rows, err, refresh } = useList<DockerNetwork>(api.networks, 15000);
  const remove = async (n: string) => { try { await api.networkRemove(n); await refresh(); } catch (e) { alert(e instanceof RequestError ? e.message : String(e)); } };
  return (
    <div className="rounded-lg border border-border bg-surface">
      {err && <div className="p-4"><Alert>{err}</Alert></div>}
      <table className="w-full text-sm">
        <thead className="text-left text-xs text-ink-muted"><tr><th className="px-4 py-2.5 font-medium">Name</th><th className="py-2.5 font-medium">Driver</th><th className="py-2.5 font-medium">Scope</th><th className="py-2.5 pr-4 text-right"></th></tr></thead>
        <tbody className="divide-y divide-border">
          {rows.map((n) => (
            <tr key={n.id}><td className="px-4 py-2 font-mono text-xs">{n.name}</td><td className="py-2 text-ink-muted">{n.driver}</td><td className="py-2 text-ink-muted">{n.scope}</td><td className="py-2 pr-4 text-right">{!["bridge", "host", "none"].includes(n.name) && <Act onClick={() => remove(n.name)} danger>Delete</Act>}</td></tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
