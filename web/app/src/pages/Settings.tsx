import { useEffect, useState, type FormEvent } from "react";
import QRCode from "qrcode";
import { api, getServer, RequestError, type Session, type ApiToken, type User, type GitHubState , type AssistantConfig } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useLocation, useSearchParams } from "react-router-dom";
import { Alert, Button, Card, Field, FieldAction, Input, Select, Tab, Tabs } from "@/components/ui";
import AuditLog from "@/components/AuditLog";
import CommandLog from "@/components/CommandLog";
import { useDialog } from "@/lib/dialogs";

const SECTIONS: { id: string; label: string; description: string; adminOnly?: boolean }[] = [
  { id: "account", label: "Account", description: "Your sign-in, your sessions and your tokens." },
  { id: "team", label: "Team", description: "Who can sign in, and what they may do.", adminOnly: true },
  { id: "panel", label: "Panel", description: "How Islet itself behaves on this server." },
  { id: "integrations", label: "Integrations", description: "Services Islet talks to on your behalf.", adminOnly: true },
  { id: "activity", label: "Activity", description: "What has happened on this server." },
];

export default function Settings() {
  const { state, refresh } = useAuth();
  const { hash } = useLocation();
  const [params, setParams] = useSearchParams();

  // A link that still carries the old anchor, such as /settings#account, picks
  // the matching tab instead of scrolling.
  useEffect(() => {
    const id = hash.replace("#", "");
    if (id && SECTIONS.some((s) => s.id === id)) {
      setParams({ tab: id }, { replace: true });
      history.replaceState(null, "", location.pathname + location.search);
    }
  }, [hash, setParams]);

  if (state.status !== "authed") return null;
  const { me } = state;
  const admin = me.user.role === "admin";
  const shown = SECTIONS.filter((s) => !s.adminOnly || admin);
  const wanted = params.get("tab") ?? "";
  const tab = shown.some((s) => s.id === wanted) ? wanted : "account";
  const current = shown.find((s) => s.id === tab)!;

  return (
    <div className="mx-auto max-w-3xl">
      <h1 className="text-xl font-semibold tracking-[-0.02em]">Settings</h1>
      <p className="mt-1 text-ink-muted">Signed in as <span className="font-medium text-ink">{me.user.username}</span>, role {me.user.role}.</p>

      <Tabs label="Settings sections" className="mt-5">
        {shown.map((sec) => (
          <Tab key={sec.id} active={tab === sec.id} onClick={() => setParams(sec.id === "account" ? {} : { tab: sec.id })}>
            {sec.label}
          </Tab>
        ))}
      </Tabs>

      <p className="mt-3 text-xs text-ink-muted">{current.description}</p>

      <div className="mt-4 space-y-4">
        {tab === "account" && <>
          <TwoFactor enabled={me.user.totpEnabled} codesLeft={me.recoveryCodesLeft} onChange={refresh} />
          <ChangePassword />
          <Sessions currentId={me.sessionId} />
          <Tokens />
        </>}

        {tab === "team" && <>
          <Users meId={me.user.id} />
          <SSO />
        </>}

        {tab === "panel" && <>
          <Updates />
          {admin && <SidebarLinks />}
          {admin && <LoginAlerts />}
          {admin && <WeeklyReport />}
        </>}

        {tab === "integrations" && <>
          <CatalogSource />
          <GitHubApp />
          <AssistantCard />
          <MCP />
        </>}

        {tab === "activity" && <>
          <CommandLog />
          <AuditLog />
        </>}
      </div>
    </div>
  );
}

function TwoFactor({ enabled, codesLeft, onChange }: { enabled: boolean; codesLeft: number; onChange: () => Promise<void> }) {
  const [setup, setSetup] = useState<{ secret: string; otpauthUrl: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function begin() {
    setError(null); setBusy(true);
    try {
      const s = await api.totpSetup();
      const qr = await QRCode.toDataURL(s.otpauthUrl, { margin: 1, width: 192 });
      setSetup({ ...s, qr });
    } catch (err) { setError(err instanceof RequestError ? err.message : "Could not start setup."); }
    finally { setBusy(false); }
  }

  async function enable(e: FormEvent) {
    e.preventDefault();
    setError(null); setBusy(true);
    try {
      const r = await api.totpEnable(code);
      setRecovery(r.codes); setSetup(null); setCode("");
      await onChange();
    } catch (err) { setError(err instanceof RequestError ? err.message : "Could not enable."); }
    finally { setBusy(false); }
  }

  async function disable(e: FormEvent) {
    e.preventDefault();
    setError(null); setBusy(true);
    try {
      await api.totpDisable(code); setCode("");
      await onChange();
    } catch (err) { setError(err instanceof RequestError ? err.message : "Could not disable."); }
    finally { setBusy(false); }
  }

  return (
    <Card title="Two-factor authentication" description={enabled ? `Enabled. ${codesLeft} recovery codes left.` : "Off. Anyone with your password can control this server."}>
      <div className="space-y-4">
        {error && <Alert>{error}</Alert>}
        {recovery && (
          <Alert tone="success">
            <p className="font-medium">Two-factor is on. Save these recovery codes now; they are shown once.</p>
            <pre className="mt-2 grid grid-cols-2 gap-x-6 font-mono text-xs">{recovery.join("\n")}</pre>
            <button type="button" className="mt-2 text-xs underline" onClick={() => setRecovery(null)}>I have saved them</button>
          </Alert>
        )}
        {!enabled && !setup && !recovery && <Button onClick={() => void begin()} disabled={busy}>Set up two-factor</Button>}
        {setup && (
          <form onSubmit={enable} className="grid grid-cols-1 gap-4 md:grid-cols-[192px_minmax(0,1fr)]">
            <img src={setup.qr} alt="QR code for your authenticator app" width={192} height={192} className="rounded-md border border-border bg-white" />
            <div className="space-y-3">
              <p className="text-sm text-ink-muted">Scan with an authenticator app, or enter the secret by hand:</p>
              <code className="block break-all rounded-md bg-surface-2 px-2 py-1 text-xs">{setup.secret}</code>
              <Field label="Code from the app">
                <Input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" required className="font-mono tracking-widest" />
              </Field>
              <div className="flex gap-2">
                <Button type="submit" disabled={busy}>Enable</Button>
                <Button type="button" variant="secondary" onClick={() => setSetup(null)}>Cancel</Button>
              </div>
            </div>
          </form>
        )}
        {enabled && (
          <form onSubmit={disable} className="flex items-start gap-2">
            <Field label="Code to disable" hint="A current app code or an unused recovery code.">
              <Input value={code} onChange={(e) => setCode(e.target.value)} required className="font-mono" />
            </Field>
            <FieldAction><Button type="submit" variant="danger" disabled={busy}>Disable two-factor</Button></FieldAction>
          </form>
        )}
      </div>
    </Card>
  );
}

function ChangePassword() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    if (next !== confirm) { setMsg({ tone: "danger", text: "New passwords do not match." }); return; }
    setBusy(true);
    try {
      await api.changePassword(current, next);
      setCurrent(""); setNext(""); setConfirm("");
      setMsg({ tone: "success", text: "Password changed. Other sessions were signed out." });
    } catch (err) { setMsg({ tone: "danger", text: err instanceof RequestError ? err.message : "Could not change password." }); }
    finally { setBusy(false); }
  }

  return (
    <Card title="Password" description="Changing it signs out every other session.">
      <form onSubmit={submit} className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {msg && <div className="md:col-span-3"><Alert tone={msg.tone}>{msg.text}</Alert></div>}
        <Field label="Current password"><Input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required autoComplete="current-password" /></Field>
        <Field label="New password"><Input type="password" value={next} onChange={(e) => setNext(e.target.value)} required minLength={12} autoComplete="new-password" /></Field>
        <Field label="Confirm new password"><Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required minLength={12} autoComplete="new-password" /></Field>
        <div className="md:col-span-3"><Button type="submit" disabled={busy}>Change password</Button></div>
      </form>
    </Card>
  );
}

function Sessions({ currentId }: { currentId: string }) {
  const [list, setList] = useState<Session[]>([]);
  const load = () => api.sessions().then(setList).catch(() => setList([]));
  useEffect(() => { void load(); }, []);

  async function revoke(id: string) {
    await api.revokeSession(id);
    if (id === currentId) window.location.reload(); else void load();
  }

  return (
    <Card title="Sessions" description="Browsers signed in to this account.">
      <ul className="divide-y divide-border">
        {list.map((s) => (
          <li key={s.id} className="flex items-center justify-between gap-4 py-2.5 text-sm">
            <div className="min-w-0">
              <div className="truncate font-medium">{s.userAgent || "Unknown client"} {s.current && <span className="ml-1 rounded-sm bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink-muted">this browser</span>}</div>
              <div className="text-xs text-ink-muted">{s.ip} · last seen {new Date(s.lastSeenAt).toLocaleString()}</div>
            </div>
            <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => void revoke(s.id)}>{s.current ? "Sign out" : "Revoke"}</Button>
          </li>
        ))}
        {list.length === 0 && <li className="py-2 text-sm text-ink-muted">No sessions.</li>}
      </ul>
    </Card>
  );
}

function Tokens() {
  const [list, setList] = useState<ApiToken[]>([]);
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [ttl, setTtl] = useState(0);
  const [created, setCreated] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => api.tokens().then(setList).catch(() => setList([]));
  useEffect(() => { void load(); }, []);
  // "shell" opens a root terminal, so it is never part of a read-only token.
  // Mirrors auth.Scopes in internal/auth/tokens.go, in the same order.
  // TestPanelOffersTheSameScopes reads this line and fails if they drift: a
  // scope offered here that no rule understands would grant nothing, and a
  // scope the rules know that is missing here cannot be given to anyone.
  const SCOPES = ["read", "deploy", "cron", "db", "containers", "domains", "files", "backups", "security", "uptime", "runners", "catalog", "workspaces", "vault", "notify", "logs", "system", "settings", "shell"];
  const create = async (e: FormEvent) => {
    e.preventDefault(); setMsg(null);
    try { const r = await api.tokenCreate({ name, scopes: scopes.length ? scopes.join(",") : "*", ttlDays: ttl }); setCreated(r.token); setName(""); setScopes([]); await load(); }
    catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); }
  };
  return (
    <Card title="API tokens" description="For the islet CLI, CI jobs and scripts. Send as Authorization: Bearer. A token acts with your role, narrowed by its scopes.">
      <ul className="divide-y divide-border">
        {list.map((t) => (
          <li key={t.id} className="flex items-center justify-between gap-4 py-2.5 text-sm">
            <div className="min-w-0"><div className="font-medium">{t.name} <span className="ml-1 font-mono text-[11px] text-ink-muted">{t.scopes}</span></div><div className="text-xs text-ink-muted">created {new Date(t.createdAt).toLocaleDateString()}{t.lastUsedAt && ` · last used ${new Date(t.lastUsedAt).toLocaleString()}`}{t.expiresAt && ` · expires ${new Date(t.expiresAt).toLocaleDateString()}`}</div></div>
            <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={async () => { await api.tokenRevoke(t.id); await load(); }}>Revoke</Button>
          </li>
        ))}
        {list.length === 0 && <li className="py-2 text-sm text-ink-muted">No tokens yet.</li>}
      </ul>
      {created && <div className="mt-3 rounded-md border border-success/40 bg-success-soft p-3 text-sm"><div className="mb-1 text-success">Copy this token now. It is not shown again.</div><pre className="overflow-x-auto font-mono text-xs">{created}</pre><pre className="mt-2 overflow-x-auto font-mono text-xs text-ink-muted">islet login --url {location.origin} --token {created}</pre></div>}
      <form onSubmit={create} className="mt-3 grid grid-cols-1 gap-3 border-t border-border pt-3 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
        <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="CI deploys" required /></Field>
        <Field label="Expires"><Select value={ttl} onChange={(e) => setTtl(+e.target.value)} className="w-auto"><option value={0}>Never</option><option value={30}>30 days</option><option value={90}>90 days</option><option value={365}>1 year</option></Select></Field>
        <FieldAction><Button type="submit" className="h-9">Create token</Button></FieldAction>
        <div className="sm:col-span-3"><span className="mb-1 block text-sm font-medium">Scopes</span><div className="flex flex-wrap gap-1"><button type="button" onClick={() => setScopes([])} className={`rounded-sm border px-2 py-0.5 text-xs ${scopes.length === 0 ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>everything</button>{SCOPES.map((sc) => <button key={sc} type="button" onClick={() => setScopes(scopes.includes(sc) ? scopes.filter((x) => x !== sc) : [...scopes, sc])} className={`rounded-sm border px-2 py-0.5 text-xs ${scopes.includes(sc) ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{sc}</button>)}</div></div>
        {msg && <p className="text-sm text-danger sm:col-span-3">{msg}</p>}
      </form>
    </Card>
  );
}

function Users({ meId }: { meId: string }) {
  // Which machine's accounts are on screen. Editing the wrong server's users
  // should not be possible by forgetting which one is selected, so it is said
  // rather than implied.
  const [where, setWhere] = useState("");
  useEffect(() => {
    const id = getServer();
    if (id === "local") { setWhere(""); return; }
    api.servers()
      .then((r) => { const s = r.servers.find((x) => x.id === id); setWhere(s ? s.name || s.host : "another server"); })
      .catch(() => setWhere("another server"));
  }, []);
  const ask = useDialog();
  const [list, setList] = useState<User[]>([]);
  const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [role, setRole] = useState("deployer");
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => api.users().then(setList).catch(() => setList([]));
  useEffect(() => { void load(); }, []);
  const create = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await api.userCreate({ username, password, role }); setUsername(""); setPassword(""); setMsg(`Created ${username}. Share the password over a safe channel; they can change it and enable 2FA in Settings.`); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const setRoleFor = async (u: User, r: string) => { try { await api.userUpdate(u.id, { role: r, password: "" }); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const setProjects = async (u: User) => { const v = await ask.prompt({ title: `What can ${u.username} work on?`, body: "App names or patterns, comma separated. Their containers, databases and domains follow the same list. Leave it empty for everything their role allows.", label: "Apps", defaultValue: u.projects ?? "", placeholder: "shop, shop-*", mono: true, confirmLabel: "Save" }); if (v === null) return; try { await api.userUpdate(u.id, { role: "", password: "", projects: v }); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const resetPw = async (u: User) => { const pw = await ask.prompt({ title: `Set a new password for ${u.username}`, body: "At least 12 characters. Every session of theirs is signed out.", label: "New password", confirmLabel: "Set password", tone: "danger" }); if (!pw) return; try { await api.userUpdate(u.id, { role: "", password: pw }); setMsg(`Password for ${u.username} changed.`); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const remove = async (u: User) => { if (!(await ask.confirm({ title: `Delete the user ${u.username}?`, body: "Every session and API token of theirs stops working immediately.", typeToConfirm: u.username, confirmLabel: "Delete user", tone: "danger" }))) return; try { await api.userDelete(u.id); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  return (
    <Card
      title={where ? `Users on ${where}` : "Users"}
      description={
        (where ? `These accounts live on ${where}, and they are the ones that sign in to its own panel and to anything it protects. ` : "") +
        "Admins do everything. Deployers can deploy, run jobs and manage containers but not change users, secrets or the host. Viewers only read. A projects list narrows a deployer or viewer to some apps and what belongs to them."
      }
    >
      <ul className="divide-y divide-border">
        {list.map((u) => (
          <li key={u.id} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
            <div><span className="font-medium">{u.username}</span>{u.id === meId && <span className="ml-1 text-xs text-ink-muted">(you)</span>}<div className="text-xs text-ink-muted">{u.isService ? "service account · no sign-in" : u.totpEnabled ? "2FA on" : "2FA off"} · {u.lastLoginAt ? `last login ${new Date(u.lastLoginAt).toLocaleString()}` : "never logged in"}</div></div>
            <div className="flex items-center gap-2 text-xs">
              <Select value={u.role} onChange={(e) => void setRoleFor(u, e.target.value)} disabled={u.id === meId} className="h-8 w-auto px-2 text-xs"><option value="admin">admin</option><option value="deployer">deployer</option><option value="viewer">viewer</option></Select>
              {u.role !== "admin" && <button type="button" onClick={() => void setProjects(u)} className="text-ink-muted hover:text-ink" title="Limit this account to some apps">{u.projects ? `projects: ${u.projects}` : "all projects"}</button>}
              <button type="button" onClick={() => void resetPw(u)} className="text-ink-muted hover:text-ink">Reset password</button>
              {u.id !== meId && <button type="button" onClick={() => void remove(u)} className="text-danger hover:underline">Delete</button>}
            </div>
          </li>
        ))}
      </ul>
      <form onSubmit={create} className="mt-3 grid grid-cols-1 gap-2 border-t border-border pt-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto]">
        <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="username" required autoComplete="off" />
        <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="password (12+ characters)" required autoComplete="new-password" />
        <Select value={role} onChange={(e) => setRole(e.target.value)} className="w-auto"><option value="admin">admin</option><option value="deployer">deployer</option><option value="viewer">viewer</option></Select>
        <Button type="submit" className="h-9">Add user</Button>
        {msg && <p className="text-xs text-ink-muted sm:col-span-4">{msg}</p>}
      </form>
    </Card>
  );
}

function SidebarLinks() {
  const [links, setLinks] = useState<{ label: string; url: string }[] | null>(null);
  const [label, setLabel] = useState(""); const [url, setUrl] = useState(""); const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { void api.sidebarManual().then(setLinks).catch(() => {}); }, []);
  if (!links) return null;
  const save = async (next: { label: string; url: string }[]) => { setMsg(null); try { setLinks(await api.sidebarSet(next)); setLabel(""); setUrl(""); setMsg("Saved. Reload to see the sidebar change."); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  return (
    <Card title="Add to sidebar" description="Show any app's own UI inside the panel. Pair it with &quot;Protect with Islet login&quot; on the app's domain so one sign-in covers both.">
      <ul className="divide-y divide-border text-sm">{links.map((l, i) => <li key={i} className="flex items-center justify-between py-1.5"><span>{l.label} <span className="ml-2 font-mono text-xs text-ink-muted">{l.url}</span></span><button type="button" onClick={() => void save(links.filter((_, j) => j !== i))} className="text-xs text-danger hover:underline">Remove</button></li>)}{links.length === 0 && <li className="py-1.5 text-xs text-ink-muted">No links yet.</li>}</ul>
      <form onSubmit={(e) => { e.preventDefault(); void save([...links, { label, url }]); }} className="mt-3 flex flex-wrap items-start gap-2 border-t border-border pt-3">
        <Field label="Label"><Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Grafana" className="w-40" required /></Field>
        <Field label="URL"><Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://grafana.example.com" className="w-72 font-mono" required /></Field>
        <FieldAction className="flex items-center gap-2"><Button type="submit" className="h-9">Add</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</FieldAction>
      </form>
    </Card>
  );
}

function LoginAlerts() {
  const [on, setOn] = useState<boolean | null>(null);
  useEffect(() => { void api.geo().then((r) => setOn(r.enabled)).catch(() => {}); }, []);
  if (on === null) return null;
  return (
    <Card title="Login alerts" description="A sign-in from an address never seen for that user raises a warning.">
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={on} onChange={async (e) => setOn((await api.geoSet(e.target.checked)).enabled)} />Add the city and country of new addresses</label>
      <p className="mt-1 text-xs text-ink-muted">Sends only that address to ipapi.co, only for new-address alerts. Off by default.</p>
    </Card>
  );
}

function WeeklyReport() {
  const [st, setSt] = useState<{ enabled: boolean; lastSent: string } | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  useEffect(() => { void api.weeklyReport().then(setSt).catch(() => {}); }, []);
  if (!st) return null;
  return (
    <Card title="Weekly report" description="Every Monday morning Islet sends a short summary: disk, security score, deploys, uptime, cron, backups. Goes to channels that accept the report category (email fits best) and always to the timeline.">
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={st.enabled} onChange={async (e) => setSt(await api.weeklyReportSet(e.target.checked))} />Send the weekly report</label>
      <div className="mt-2 flex items-center gap-2 text-xs text-ink-muted">
        <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={async () => { const r = await api.weeklyReportSend(); setPreview(r.body); }}>Send one now</Button>
        {st.lastSent && <span>Last sent {new Date(st.lastSent).toLocaleString()}</span>}
      </div>
      {preview !== null && <pre className="mt-3 overflow-x-auto whitespace-pre-wrap rounded-md border border-border bg-bg p-2 font-mono text-xs">{preview || "Nothing to report yet."}</pre>}
    </Card>
  );
}

function SSO() {
  const [d, setD] = useState<string | null>(null); const [v, setV] = useState(""); const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { void api.cookieDomain().then((r) => { setD(r.cookieDomain); setV(r.cookieDomain); }).catch(() => {}); }, []);
  if (d === null) return null;
  const save = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { const r = await api.cookieDomainSet(v); setD(r.cookieDomain); setMsg(r.cookieDomain ? `Protected sites under *.${r.cookieDomain} can now see that you are signed in, including from the session you are using. Set the access rules on a domain under it.` : "Nothing under a parent domain can see this panel's sign-ins any more."); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  return (
    <Card title="Protect apps with Islet login" description="Route the panel to a domain (say panel.example.com), set the parent domain here, and any domain marked &quot;Protect with Islet login&quot; only opens for people signed in to this panel.">
      <form onSubmit={save} className="flex flex-wrap items-start gap-2">
        <Field label="Session cookie domain" hint="The parent of the panel and the protected apps, for example example.com. A browser hands every site under this name a cookie saying who is signed in — it is not the panel's own session, which never leaves this host, but it does name you."><Input value={v} onChange={(e) => setV(e.target.value)} className="w-64 font-mono" placeholder="example.com" /></Field>
        <FieldAction className="flex items-center gap-2"><Button type="submit" className="h-9">Save</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</FieldAction>
      </form>
    </Card>
  );
}

function CatalogSource() {
  const [st, setSt] = useState<{ source: { url: string; fetchedAt: string; apps: number; recipes: number; lastError?: string }; default: string } | null>(null);
  const [url, setUrl] = useState(""); const [busy, setBusy] = useState(false); const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { void api.catalogSource().then((r) => { setSt(r); setUrl(r.source.url || r.default); }).catch(() => {}); }, []);
  if (!st) return null;
  const refresh = async () => { setBusy(true); setMsg(null); try { const r = await api.catalogSourceSet(url); setSt(r); setMsg(`Fetched ${r.source.apps} apps and ${r.source.recipes} recipes.`); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } finally { setBusy(false); } };
  const clear = async () => { setBusy(true); try { await api.catalogSourceClear(); setSt(await api.catalogSource()); setMsg("Back to the embedded catalog."); } finally { setBusy(false); } };
  return (
    <Card title="Catalog source" description="The catalog ships inside the daemon and works offline. Point it at the public catalog repository (or your own fork) to pick up new apps and recipes daily without updating Islet; fetched templates overlay the embedded ones.">
      <div className="flex flex-wrap items-start gap-2">
        {/* basis with min-w-0, not a fixed width: a flex item's min-width is
            its content by default, so a 28rem input in a wrapping row refuses
            to shrink and hangs off the side of a phone — clipped rather than
            scrollable, which is the worst of both. */}
        <Field label="Tarball URL" hint="A .tar.gz with apps/ and recipes/; GitHub archive links work." className="min-w-0 basis-[28rem]"><Input value={url} onChange={(e) => setUrl(e.target.value)} className="w-full font-mono" /></Field>
        <FieldAction className="flex items-center gap-2"><Button variant="secondary" className="h-9" disabled={busy} onClick={() => void refresh()}>{busy ? "Fetching…" : "Fetch now"}</Button>{st.source.url && <button type="button" onClick={() => void clear()} className="text-xs text-danger hover:underline">Use embedded only</button>}</FieldAction>
      </div>
      <p className="mt-2 text-xs text-ink-muted">{st.source.fetchedAt ? `Last fetched ${new Date(st.source.fetchedAt).toLocaleString()} (${st.source.apps} apps, ${st.source.recipes} recipes).` : "Not fetched yet; refreshes daily once set."}{st.source.lastError && <span className="text-danger"> Last error: {st.source.lastError}</span>}{msg && <span> {msg}</span>}</p>
    </Card>
  );
}

function GitHubApp() {
  const ask = useDialog();
  const [st, setSt] = useState<GitHubState | null>(null);
  const [form, setForm] = useState({ appId: "", clientId: "", slug: "", privateKey: "", webhookSecret: "" });
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const load = () => api.github().then((s) => { setSt(s); setForm((f) => ({ ...f, appId: s.config.appId, clientId: s.config.clientId, slug: s.config.slug })); }).catch(() => {});
  useEffect(() => { void load(); }, []);
  const save = async (e: FormEvent) => { e.preventDefault(); setBusy(true); setMsg(null); try { await api.githubSave(form); setForm((f) => ({ ...f, privateKey: "", webhookSecret: "" })); setMsg("Saved and verified with GitHub."); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } finally { setBusy(false); } };
  const clear = async () => { if (!(await ask.confirm({ title: "Remove the GitHub App?", body: "Repository pickers stop listing private repositories, and apps and runners fall back to personal access tokens.", confirmLabel: "Remove", tone: "danger" }))) return; await api.githubSave({ appId: "", clientId: "", slug: "", privateKey: "", webhookSecret: "" }); await load(); };
  if (!st) return null;
  return (
    <Card title="GitHub App" description="Lets people pick repositories from a list, clones private repositories with short-lived tokens, registers runners without personal access tokens, and receives one webhook for pushes and CI jobs.">
      {st.config.configured && (
        <div className="mb-3 rounded-md border border-success/40 bg-success-soft p-3 text-sm">
          <div className="text-success">Configured as App {st.config.appId}{st.installations && ` · installed on ${st.installations.map((i) => i.account).join(", ") || "nobody yet"}`}</div>
          {st.error && <div className="mt-1 text-danger">{st.error}</div>}
          <div className="mt-1 text-xs text-ink-muted">Webhook URL for the app: <span className="font-mono">{location.origin}{st.hookUrl}</span> (events: push, workflow_job). {st.config.slug && <>Install it on more accounts at <a className="underline" href={`https://github.com/apps/${st.config.slug}/installations/new`} target="_blank" rel="noreferrer">github.com/apps/{st.config.slug}</a>.</>}</div>
          <button type="button" onClick={() => void clear()} className="mt-2 text-xs text-danger hover:underline">Remove</button>
        </div>
      )}
      <form onSubmit={save} className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <Field label="App ID"><Input value={form.appId} onChange={(e) => setForm({ ...form, appId: e.target.value })} className="font-mono" required /></Field>
        <Field label="Client ID"><Input value={form.clientId} onChange={(e) => setForm({ ...form, clientId: e.target.value })} className="font-mono" /></Field>
        <Field label="App slug" hint="From the app URL, github.com/apps/<slug>"><Input value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} className="font-mono" /></Field>
        <div className="sm:col-span-2"><Field label="Private key (.pem)" hint={st.config.configured ? "Leave empty to keep the stored key." : "Generate one at the bottom of the GitHub App page and paste the file contents."}><textarea value={form.privateKey} onChange={(e) => setForm({ ...form, privateKey: e.target.value })} rows={4} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" /></Field></div>
        <Field label="Webhook secret" hint={st.config.configured ? "Leave empty to keep it." : "The secret you typed on the GitHub App page."}><Input type="password" value={form.webhookSecret} onChange={(e) => setForm({ ...form, webhookSecret: e.target.value })} autoComplete="off" /></Field>
        <div className="flex items-center gap-2 sm:col-span-3"><Button type="submit" className="h-9" disabled={busy}>{busy ? "Verifying…" : "Save"}</Button>{msg && <span className="text-sm text-ink-muted">{msg}</span>}</div>
      </form>
    </Card>
  );
}

function AssistantCard() {
  const [c, setC] = useState<AssistantConfig | null>(null);
  const [key, setKey] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => { void api.assistant().then(setC).catch(() => {}); };
  useEffect(load, []);
  if (!c) return null;
  const save = async (e: FormEvent) => {
    e.preventDefault(); setMsg(null);
    try {
      await api.assistantSave({ provider: c.provider, model: c.model, baseUrl: c.baseUrl, key, mcpConfig: c.mcpConfig });
      setKey(""); setMsg("Saved."); load();
    } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); }
  };
  return (
    <Card title="Assistant" description="The model behind Ask. It acts as whoever is asking and can do no more than they can.">
      <form onSubmit={save} className="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <label className="sm:col-span-3">
          <span className="mb-1 block text-sm font-medium">Where the model runs</span>
          <Select value={c.provider} onChange={(e) => setC({ ...c, provider: e.target.value as AssistantConfig["provider"] })}>
            <option value="anthropic">Anthropic — an API key, billed per token</option>
            <option value="subscription">Claude subscription on this server{c.claudeInstalled ? "" : " (claude is not installed)"}</option>
            <option value="openai">Any OpenAI-compatible API — OpenAI, Groq, OpenRouter, or a local model</option>
          </Select>
        </label>
        <Input value={c.model} onChange={(e) => setC({ ...c, model: e.target.value })} placeholder={c.provider === "anthropic" ? c.defaultModel : "model name"} className="font-mono" aria-label="Model" />
        {c.provider === "openai" && (
          <Input value={c.baseUrl} onChange={(e) => setC({ ...c, baseUrl: e.target.value })} placeholder="https://api.openai.com/v1" className="font-mono sm:col-span-2" aria-label="Base URL" />
        )}
        {c.provider === "subscription" && (
          <Input value={c.mcpConfig} onChange={(e) => setC({ ...c, mcpConfig: e.target.value })} placeholder="/var/lib/islet/workspaces/<id>/mcp.json" className="font-mono sm:col-span-2" aria-label="MCP config path" />
        )}
        {c.provider !== "subscription" && (
          <Input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder={c.keySet ? "key is set — leave blank to keep it" : "API key"} className="font-mono sm:col-span-2" aria-label="API key" />
        )}
        <Button type="submit" className="h-9 text-xs">Save</Button>
        <p className="text-xs text-ink-muted sm:col-span-3">
          {c.tools} tools are available to it. A subscription runs Claude Code on this server and is bounded by the token in its MCP configuration, not by who is asking.
          {c.provider === "subscription" && (
            <>
              {" "}That token is the only fence around it: Claude Code&apos;s own tools are denied, so everything it can do,
              it does through Islet under those scopes, and every call is in the audit log. A token with every scope means
              an assistant with every scope. Issue a narrower one under API tokens below if that is more authority than
              you meant to hand it.
            </>
          )}
        </p>
        {msg && <p className="text-sm text-ink-muted sm:col-span-3">{msg}</p>}
      </form>
    </Card>
  );
}

function MCP() {
  const [st, setSt] = useState<{ enabled: boolean; url: string } | null>(null);
  useEffect(() => { void api.mcp().then(setSt).catch(() => {}); }, []);
  if (!st) return null;
  return (
    <Card title="MCP server" description="Lets an AI agent (Claude, Cursor, any MCP client) read this server and, with the right token scopes, deploy, run jobs and notify. Off by default.">
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={st.enabled} onChange={async (e) => { const r = await api.mcpSet(e.target.checked); setSt({ enabled: r.enabled, url: "/mcp" }); }} />Enable the MCP endpoint</label>
      {st.enabled && <pre className="mt-3 overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-xs">{`{ "mcpServers": { "islet": { "url": "${location.origin}/mcp", "headers": { "Authorization": "Bearer islet_…" } } } }`}</pre>}
      {st.enabled && <p className="mt-2 text-xs text-ink-muted">Use an API token with only the scopes the agent needs; read-only is a good start. Every call is audited.</p>}
    </Card>
  );
}

function Updates() {
  const [status, setStatus] = useState<{ current: string; latest: string; updateAvailable: boolean; publishedAt: string } | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function check() {
    setBusy(true); setMsg(null);
    try { setStatus(await api.updateCheck()); }
    catch (err) { setMsg(err instanceof RequestError ? err.message : "Could not check."); }
    finally { setBusy(false); }
  }
  async function apply() {
    setBusy(true); setMsg(null);
    try {
      const r = await api.updateApply();
      setMsg(`Installed ${r.to}. The daemon is restarting; reload in a few seconds.`);
    } catch (err) { setMsg(err instanceof RequestError ? err.message : "Update failed."); }
    finally { setBusy(false); }
  }

  return (
    <Card title="Updates" description="Releases are signed. Islet refuses anything it cannot verify.">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="secondary" onClick={() => void check()} disabled={busy}>Check for updates</Button>
        {status && (
          <span className="text-sm text-ink-muted">
            {status.updateAvailable ? `Update available: ${status.current} → ${status.latest}` : `Up to date (${status.current}, latest ${status.latest})`}
          </span>
        )}
        {status?.updateAvailable && <Button onClick={() => void apply()} disabled={busy}>Install {status.latest}</Button>}
      </div>
      {msg && <p className="mt-3 text-sm text-ink-muted">{msg}</p>}
    </Card>
  );
}
