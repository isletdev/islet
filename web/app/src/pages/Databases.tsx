import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, RequestError, type DBDetail, type DBInstance } from "@/lib/api";
import { postStream } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, FieldAction, Input, Select } from "@/components/ui";
import AppIcon from "@/components/AppIcon";
import { capLines } from "@/lib/logcap";
import { useDialog } from "@/lib/dialogs";

const ENGINE: Record<string, string> = { postgres: "PostgreSQL", mysql: "MySQL", redis: "Redis", mongo: "MongoDB" };
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }
function bytes(n: number) { return n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : n < 1073741824 ? `${(n / 1048576).toFixed(1)} MB` : `${(n / 1073741824).toFixed(2)} GB`; }
function err(e: unknown) { return e instanceof RequestError ? e.message : e instanceof Error ? e.message : String(e); }

function Copy({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return <button type="button" onClick={() => { void navigator.clipboard.writeText(text); setDone(true); setTimeout(() => setDone(false), 1500); }} className="text-xs text-ink-muted hover:text-ink">{done ? "Copied" : label}</button>;
}

export default function Databases() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const [list, setList] = useState<DBInstance[]>([]);
  const [error, setError] = useState<string | null>(null);
  const selected = params.get("i");
  const load = useCallback(() => api.databases().then((l) => { setList(l); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); }, [load]);

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Databases</h1>
          <p className="mt-1 text-ink-muted">Every database server installed from the catalog, with connection strings, dumps and health.</p>
        </div>
        <Button type="button" onClick={() => nav("/apps?tab=catalog&category=database")}>Install a database</Button>
      </div>
      {error && <Alert>{error}</Alert>}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {list.map((i) => (
          <button key={i.name} type="button" onClick={() => setParams({ i: i.name })} className={`rounded-lg border p-4 text-left transition-colors ${selected === i.name ? "border-ink bg-surface-2" : "border-border bg-surface hover:bg-surface-2"}`}>
            <div className="flex items-center gap-2.5">
              <AppIcon slug={i.engine} category="database" name={ENGINE[i.engine]} size="sm" />
              <span className="min-w-0 flex-1 truncate font-semibold">{i.name}</span>
              <span className={`h-2 w-2 shrink-0 rounded-full ${i.state === "running" ? "bg-success" : i.state === "missing" ? "bg-ink-faint" : "bg-danger"}`} />
            </div>
            <div className="mt-2 text-xs text-ink-muted">{ENGINE[i.engine]} · {i.image.split("@")[0]}</div>
            <div className="mt-2 font-mono text-[11px] text-ink-faint">{i.container}:{i.port}{i.public && <span className="ml-2 rounded-sm bg-warning-soft px-1 text-warning">public</span>}</div>
          </button>
        ))}
        {list.length === 0 && !error && (
          <div className="col-span-full rounded-lg border border-dashed border-border p-8 text-center">
            <div className="flex justify-center gap-2">
              {["postgres", "mysql", "mariadb", "redis", "mongo"].map((e) => <AppIcon key={e} slug={e} category="database" name={e} size="sm" />)}
            </div>
            <p className="mt-3 text-sm text-ink-muted">No databases yet. Install Postgres, MySQL, MariaDB, Redis or MongoDB and it appears here.</p>
            <Button className="mt-3" type="button" onClick={() => nav("/apps?tab=catalog&category=database")}>Install a database</Button>
          </div>
        )}
      </div>
      {selected && <Detail name={selected} isAdmin={isAdmin} onChanged={load} />}
    </div>
  );
}

function Detail({ name, isAdmin, onChanged }: { name: string; isAdmin: boolean; onChanged: () => Promise<void> }) {
  const ask = useDialog();
  const [d, setD] = useState<DBDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [log, setLog] = useState<string[] | null>(null);
  const [showSecrets, setShowSecrets] = useState(false);
  const [slow, setSlow] = useState<{ query: string; calls: number; meanMs: number }[] | null>(null);
  const [adminer, setAdminer] = useState<{ suggestedHost: string; panelHost?: string; cookieDomain?: string } | null>(null);
  const load = useCallback(() => api.database(name).then((x) => { setD(x); setError(null); }).catch((e) => setError(err(e))), [name]);
  useEffect(() => { setD(null); setMsg(null); setLog(null); setSlow(null); void load(); }, [load]);

  const act = async (key: string, fn: () => Promise<unknown>, done?: string) => {
    setBusy(key); setMsg(null);
    try { await fn(); if (done) setMsg(done); await load(); } catch (e) { setMsg(err(e)); } finally { setBusy(null); }
  };
  const togglePublic = async (on: boolean) => {
    let hostPort = d?.port ?? 0;
    let allowFrom = "";
    let bind = "";
    if (on) {
      const toInternet = await ask.confirm({
        title: `Who should reach port ${d?.port}?`,
        body: "Bound to this server, only something already on the machine reaches it, which is what an SSH tunnel uses. Open to the internet, anyone can connect and try passwords.",
        cancelLabel: "This server only",
        confirmLabel: "Open to the internet",
        tone: "danger",
      });
      bind = toInternet ? "" : "127.0.0.1";
      if (toInternet) {
        allowFrom = (await ask.prompt({
          title: "Which addresses may connect?",
          body: "Addresses or ranges, comma separated. Leaving it empty lets anyone try. The list is only enforced while the firewall is on, which is on the Security page.",
          label: "Allowed addresses",
          defaultValue: d?.allowFrom ?? "",
          placeholder: "203.0.113.4, 198.51.100.0/24",
          mono: true,
          confirmLabel: "Continue",
        })) ?? "";
      }
      const v = await ask.prompt({
        title: "Which port on the server?",
        body: toInternet ? "This port will answer on every network interface." : "This port answers on 127.0.0.1 only, so an SSH tunnel is the way in.",
        label: "Host port",
        defaultValue: String(d?.port),
        mono: true,
        confirmLabel: "Publish",
      });
      if (!v) return;
      hostPort = +v;
    }
    setBusy("public"); setLog([]);
    try { await postStream(`/api/v1/databases/${name}/public`, (l) => setLog((p) => capLines(p, l)), { public: on, hostPort, allowFrom, bind }); await load(); await onChanged(); } catch (e) { setMsg(err(e)); } finally { setBusy(null); }
  };
  const openAdminer = async (setup?: { host: string; tls: string; protect: boolean }) => {
    setBusy("adminer"); setMsg(null);
    try {
      const r = await api.dbAdminer(name, setup);
      if (r.installed) setMsg(`Adminer installed at ${r.domain}. The certificate takes a moment.`);
      if (r.url) {
        window.open(r.url, "_blank", "noopener");
        if (r.password) await navigator.clipboard?.writeText(r.password).catch(() => {});
        setMsg((m) => `${m ? m + " " : ""}Opened Adminer; the password is on your clipboard.`);
      }
    } catch (e) {
      const body = e instanceof RequestError ? (e.body as { setupRequired?: boolean; suggestedHost?: string; panelHost?: string; cookieDomain?: string } | undefined) : undefined;
      if (body?.setupRequired) setAdminer({ suggestedHost: body.suggestedHost ?? "", panelHost: body.panelHost, cookieDomain: body.cookieDomain });
      else setMsg(err(e));
    } finally { setBusy(null); }
  };
  const mask = (s: string) => showSecrets ? s : s.replace(/:\/\/([^:@]+):([^@]+)@/, "://$1:••••••••@");

  if (error) return <Alert>{error}</Alert>;
  if (!d) return <p className="text-sm text-ink-muted">Loading {name}…</p>;
  const sqlish = d.engine === "postgres" || d.engine === "mysql" || d.engine === "mongo";
  return (
    <div className="space-y-4">
      <Card title={`${d.name} · ${ENGINE[d.engine]}`} description={d.stats ? `${d.stats.version} · up ${d.stats.uptime} · ${d.stats.connections}/${d.stats.maxConnections} connections · ${d.stats.dataSize || "size n/a"}` : d.state === "running" ? "Collecting stats…" : `Container is ${d.state}. Start it from Containers.`}>
        {d.error && <Alert>{d.error}</Alert>}
        {d.stats?.extra && <p className="mb-3 text-xs text-ink-muted">{d.stats.extra.join(" · ")}</p>}
        {isAdmin && d.engine !== "redis" && <p className="mb-3 text-xs"><button type="button" disabled={!!busy} onClick={() => void openAdminer()} className="text-accent hover:underline">{busy === "adminer" ? "Preparing Adminer…" : "Open in Adminer"}</button><span className="ml-2 text-ink-muted">One Adminer serves every database here; it is attached to this one's network when you open it.</span></p>}
        {adminer && <AdminerSetup s={adminer} onCancel={() => setAdminer(null)} onSubmit={(v) => { setAdminer(null); void openAdminer(v); }} />}
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <div className="mb-1 flex items-center justify-between text-xs"><span className="font-medium">From other containers</span>{isAdmin && d.internalUrl && <Copy text={d.internalUrl} />}</div>
            <pre className="overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-xs">{isAdmin ? mask(d.internalUrl) : "(admins only)"}</pre>
            {d.engine === "postgres" && isAdmin && <div className="mt-2 text-xs">
              <button type="button" disabled={busy === "pooler"} onClick={async () => { setBusy("pooler"); setLog([]); setMsg(null); try { await postStream(`/api/v1/databases/${name}/pooler`, (l) => setLog((p) => capLines(p, l)), { enabled: !d.pooler }); await load(); } catch (e) { setMsg(err(e)); } finally { setBusy(null); } }} className={d.pooler ? "text-ink-muted hover:text-ink" : "text-accent hover:underline"}>{busy === "pooler" ? "Working…" : d.pooler ? "Remove PgBouncer" : "Add PgBouncer connection pooling"}</button>
              <span className="ml-2 text-ink-muted">{d.pooler ? "Transaction pooling, 20 server connections shared by up to 1000 clients." : "For apps that open many short connections (serverless, PHP, many workers)."}</span>
              {d.pooler && d.pooledUrl && <pre className="mt-1 overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-xs">{mask(d.pooledUrl)}</pre>}
            </div>}
            <p className="mt-1 text-xs text-ink-muted">Attach the other stack to the <span className="font-mono">{d.network}</span> network, or connect from a stack on the same network by service name.</p>
          </div>
          <div>
            <div className="mb-1 flex items-center justify-between text-xs"><span className="font-medium">{d.public ? "From the internet" : "From your machine"}</span>{isAdmin && d.publicUrl && <Copy text={d.publicUrl} />}</div>
            {d.public ? <><pre className="overflow-x-auto rounded-md border border-warning/40 bg-bg p-2 font-mono text-xs">{isAdmin ? mask(d.publicUrl ?? "") : "(admins only)"}</pre>{d.allowFrom && <p className="mt-1 text-xs text-ink-muted">Firewall allows only: <span className="font-mono">{d.allowFrom}</span></p>}</>
              : <pre className="overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-xs">ssh -N -L {d.port}:{d.ip || "CONTAINER-IP"}:{d.port} you@your-server{"\n"}# then point the client at localhost:{d.port}{"\n"}# this address moves when the container is recreated; publishing the port{"\n"}# on 127.0.0.1 gives a tunnel target that does not.</pre>}
            {isAdmin && <div className="mt-1 flex items-center gap-3 text-xs">
              <button type="button" onClick={() => setShowSecrets(!showSecrets)} className="text-ink-muted hover:text-ink">{showSecrets ? "Hide passwords" : "Show passwords"}</button>
              <button type="button" disabled={busy === "public"} onClick={() => void togglePublic(!d.public)} className={d.public ? "text-ink-muted hover:text-ink" : "text-warning hover:underline"}>{d.public ? "Stop publishing the port" : "Publish the port to the internet…"}</button>
            </div>}
            {d.public && <p className="mt-1 text-xs text-warning">Published on every interface. An IP allowlist arrives with the firewall in v0.6; until then use strong passwords.</p>}
          </div>
        </div>
        {log && <pre className="mt-3 max-h-40 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-2 font-mono text-xs text-[#FAFAFA]">{log.join("\n") || "…"}</pre>}
        {msg && <p className="mt-3 text-sm text-ink-muted">{msg}</p>}
      </Card>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card title={d.engine === "redis" ? "Keyspaces" : "Databases"} description={sqlish ? "Each database gets its own user with full rights on it." : undefined}>
          <table className="w-full text-sm"><tbody className="divide-y divide-border">
            {d.databases.map((x) => (
              <tr key={x.name}><td className="py-1.5 font-medium">{x.name}{x.owner && <span className="ml-2 text-xs text-ink-muted">{x.owner}</span>}</td><td className="py-1.5 text-xs text-ink-muted">{x.size}{x.connections > 0 && ` · ${x.connections} conn`}</td>
                <td className="py-1.5 text-right text-xs whitespace-nowrap">{isAdmin && d.engine !== "redis" && <><button type="button" disabled={!!busy} onClick={() => void act("dump" + x.name, () => api.dbDump(name, x.name), `Dumped ${x.name}.`)} className="text-ink-muted hover:text-ink">{busy === "dump" + x.name ? "Dumping…" : "Dump now"}</button><button type="button" disabled={!!busy} onClick={async () => { if (await ask.confirm({ title: `Drop the database ${x.name}?`, body: "The database and its user are removed. Everything in it is gone and this cannot be undone.", typeToConfirm: x.name, confirmLabel: "Drop database", tone: "danger" })) void act("drop", () => api.dbDrop(name, x.name)); }} className="ml-3 text-danger hover:underline">Drop</button></>}</td></tr>
            ))}
            {d.databases.length === 0 && <tr><td className="py-2 text-ink-muted">{d.state === "running" ? "Nothing to list." : "Instance is not running."}</td></tr>}
          </tbody></table>
          {isAdmin && d.engine === "redis" && <div className="mt-2"><Button variant="secondary" className="h-8 text-xs" disabled={!!busy} onClick={() => void act("dumpredis", () => api.dbDump(name, "redis"), "Snapshot saved.")}>Snapshot now</Button></div>}
          {isAdmin && sqlish && <CreateForm name={name} onDone={load} />}
        </Card>

        <Card title="Dumps" description="Gzipped, stored on this server. Schedule nightly dumps to get failure alerts.">
          {isAdmin && <ScheduleForm d={d} onDone={async () => { await load(); }} />}
          <ul className="mt-3 divide-y divide-border text-xs">
            {d.dumps.map((f) => (
              <li key={f.file} className="flex flex-wrap items-center justify-between gap-2 py-1.5"><span className="font-mono">{f.file}</span><span className="text-ink-muted">{bytes(f.size)} · {fmt(f.createdAt)}</span>
                {isAdmin && <span className="flex gap-3"><a href={`/api/v1/databases/${name}/dumps/${f.file}`} className="text-ink-muted hover:text-ink">Download</a>{d.engine !== "redis" && <button type="button" disabled={!!busy} onClick={async () => { const into = await ask.prompt({ title: "Restore this dump", body: "The database is created if it is missing. If it already exists, objects in it with the same names are replaced, so restoring into a live database overwrites it.", label: "Restore into", defaultValue: f.database, mono: true, confirmLabel: "Restore", tone: "danger" }); if (into) void act("restore", () => api.dbRestore(name, f.file, into), `Restored ${f.file} into ${into}.`); }} className="text-ink-muted hover:text-ink">Restore…</button>}<button type="button" onClick={() => void act("del", () => api.dbDumpDelete(name, f.file))} className="text-danger hover:underline">Delete</button></span>}</li>
            ))}
            {d.dumps.length === 0 && <li className="py-2 text-ink-muted">No dumps yet. Dump now takes a logical copy you can download; scheduled dumps are cron jobs, and Backups keeps encrypted snapshots off-site.</li>}
          </ul>
        </Card>
      </div>

      {d.engine === "postgres" && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Card title="Extensions" description={`Toggled in the ${d.database} database.`}>
            <ul className="divide-y divide-border text-sm">
              {d.extensions.map((e) => (
                <li key={e.name} className="flex items-center justify-between gap-3 py-1.5"><div><span className="font-mono text-xs">{e.name}</span><div className="text-xs text-ink-muted">{e.comment}</div></div>
                  {isAdmin && e.available ? <button type="button" disabled={!!busy} onClick={() => void act("ext", () => api.dbExtension(name, e.name, !e.installed))} className={`rounded-sm border px-2 py-0.5 text-xs ${e.installed ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{e.installed ? "on" : "off"}</button> : <span className="text-xs text-ink-faint">{e.available ? (e.installed ? "on" : "off") : "not in image"}</span>}</li>
              ))}
            </ul>
          </Card>
          <Card title="Slow queries" description="From pg_stat_statements. Turn the extension on and restart the instance to collect data.">
            {slow === null ? <Button variant="secondary" className="h-8 text-xs" onClick={async () => { try { setSlow(await api.dbSlow(name)); } catch (e) { setMsg(err(e)); } }}>Load</Button>
              : slow.length === 0 ? <p className="text-sm text-ink-muted">No statistics yet.</p>
              : <ul className="divide-y divide-border text-xs">{slow.map((q, i) => <li key={i} className="py-1.5"><div className="font-mono break-all">{q.query}</div><div className="text-ink-muted">{q.calls} calls · {q.meanMs} ms mean</div></li>)}</ul>}
          </Card>
        </div>
      )}
    </div>
  );
}

function CreateForm({ name, onDone }: { name: string; onDone: () => Promise<void> }) {
  const [db, setDb] = useState(""); const [user, setUser] = useState(""); const [pw, setPw] = useState("");
  const [result, setResult] = useState<string | null>(null); const [msg, setMsg] = useState<string | null>(null);
  const submit = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { const r = await api.dbCreate(name, { name: db, user, password: pw }); setResult(r.url); setDb(""); setUser(""); setPw(""); await onDone(); } catch (er) { setMsg(err(er)); } };
  return (
    <form onSubmit={submit} className="mt-3 border-t border-border pt-3">
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3"><Input value={db} onChange={(e) => setDb(e.target.value)} placeholder="database name" required /><Input value={user} onChange={(e) => setUser(e.target.value)} placeholder="user (defaults to name)" /><Input value={pw} onChange={(e) => setPw(e.target.value)} placeholder="password (generated if empty)" /></div>
      <div className="mt-2 flex items-center gap-2"><Button type="submit" className="h-8 text-xs">Create database and user</Button>{msg && <span className="text-xs text-danger">{msg}</span>}</div>
      {result && <div className="mt-2"><div className="mb-1 flex items-center justify-between text-xs"><span className="text-ink-muted">Created. Copy the URL now; the password is not stored by Islet.</span><Copy text={result} /></div><pre className="overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-xs">{result}</pre></div>}
    </form>
  );
}

function ScheduleForm({ d, onDone }: { d: DBDetail; onDone: () => Promise<void> }) {
  const [schedule, setSchedule] = useState(d.dumpJob?.schedule ?? "0 3 * * *");
  const [keep, setKeep] = useState(14);
  const [msg, setMsg] = useState<string | null>(null);
  const save = async (enabled: boolean) => { setMsg(null); try { await api.dbSchedule(d.name, { schedule, keepDays: keep, enabled }); setMsg(enabled ? "Scheduled. Failures raise a cron alert." : "Paused."); await onDone(); } catch (e) { setMsg(err(e)); } };
  return (
    <div className="rounded-md border border-border p-3 text-sm">
      <div className="flex flex-wrap items-start gap-2">
        <Field label="Schedule"><Input value={schedule} onChange={(e) => setSchedule(e.target.value)} className="w-36 font-mono" /></Field>
        <Field label="Keep days"><Input type="number" min={1} value={keep} onChange={(e) => setKeep(+e.target.value)} className="w-24" /></Field>
        <FieldAction className="flex items-center gap-2"><Button className="h-9 text-xs" onClick={() => void save(true)}>{d.dumpJob?.enabled ? "Update schedule" : "Schedule dumps"}</Button>{d.dumpJob?.enabled && <Button variant="secondary" className="h-9 text-xs" onClick={() => void save(false)}>Pause</Button>}</FieldAction>
      </div>
      <p className="mt-2 text-xs text-ink-muted">{d.dumpJob ? <>Job <Link to={`/cron?job=${d.dumpJob.id}`} className="underline">{d.dumpJob.name}</Link> · {d.dumpJob.described} · {d.dumpJob.enabled ? (d.dumpJob.lastRun ? `last run ${d.dumpJob.lastRun.status}` : "not run yet") : "paused"}. Edit the script in Cron to add S3 upload.</> : "Creates a script job in Cron that dumps every database and prunes old files."}</p>
      {msg && <p className="mt-1 text-xs text-ink-muted">{msg}</p>}
    </div>
  );
}

/** Asked once, the first time anyone opens Adminer: where it should answer,
 * what certificate it gets, and whether the panel's login guards it. */
function AdminerSetup({ s, onCancel, onSubmit }: { s: { suggestedHost: string; panelHost?: string; cookieDomain?: string }; onCancel: () => void; onSubmit: (v: { host: string; tls: string; protect: boolean }) => void }) {
  const [host, setHost] = useState(s.suggestedHost);
  const [tls, setTls] = useState(s.suggestedHost.endsWith(".sslip.io") ? "self" : "letsencrypt");
  const [protect, setProtect] = useState(true);
  const parent = s.panelHost ? s.panelHost.split(".").slice(-2).join(".") : "";
  const cookieOk = !protect || (!!s.cookieDomain && host.endsWith(s.cookieDomain));
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit({ host: host.trim(), tls, protect }); }} className="mb-3 grid grid-cols-1 gap-3 rounded-md border border-border p-3 sm:grid-cols-2">
      <p className="text-xs text-ink-muted sm:col-span-2">Adminer is not installed yet. It runs as one small container for every database on this server, reachable only through the proxy on the name you choose.</p>
      <Field label="Host" hint={host.endsWith(".sslip.io") ? "A free name that resolves to this server. Let's Encrypt limits certificates per registered domain and every sslip.io user shares one, so a name under your own domain is far more likely to get a real certificate." : "An A record for this name must point at this server."}>
        <Input value={host} onChange={(e) => setHost(e.target.value)} className="font-mono" required />
      </Field>
      <Field label="Certificate">
        <Select value={tls} onChange={(e) => setTls(e.target.value)}>
          <option value="letsencrypt">Let's Encrypt</option>
          <option value="self">Self-signed</option>
          <option value="none">HTTP only</option>
        </Select>
      </Field>
      <label className="flex items-center gap-1.5 text-sm sm:col-span-2"><input type="checkbox" checked={protect} onChange={(e) => setProtect(e.target.checked)} />Only reachable by people signed in to this panel</label>
      {!cookieOk && <p className="text-xs text-warning sm:col-span-2">That guard needs the session cookie domain set to a parent of both the panel and this host{parent && <> (probably <span className="font-mono">{parent}</span>)</>}: Settings, Protect apps with Islet login. Without it you will be sent to the login page in a loop.</p>}
      <div className="flex items-center gap-2 sm:col-span-2"><Button type="submit">Install Adminer</Button><Button type="button" variant="secondary" onClick={onCancel}>Cancel</Button></div>
    </form>
  );
}
