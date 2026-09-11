import { useEffect, useState, type FormEvent } from "react";
import QRCode from "qrcode";
import { api, RequestError, type Session, type ApiToken, type User, type GitHubState } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";
import AuditLog from "@/components/AuditLog";
import CommandLog from "@/components/CommandLog";

export default function Settings() {
  const { state, refresh } = useAuth();
  if (state.status !== "authed") return null;
  const { me } = state;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Settings</h1>
        <p className="mt-1 text-ink-muted">Signed in as <span className="font-medium text-ink">{me.user.username}</span>, role {me.user.role}.</p>
      </div>
      <TwoFactor enabled={me.user.totpEnabled} codesLeft={me.recoveryCodesLeft} onChange={refresh} />
      <ChangePassword />
      <Sessions currentId={me.sessionId} />
      <Tokens />
      {me.user.role === "admin" && <Users meId={me.user.id} />}
      <Updates />
      {me.user.role === "admin" && <GitHubApp />}
      {me.user.role === "admin" && <MCP />}
      <CommandLog />
      <AuditLog />
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
          <form onSubmit={enable} className="grid gap-4 md:grid-cols-[192px_1fr]">
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
          <form onSubmit={disable} className="flex items-end gap-2">
            <Field label="Code to disable" hint="A current app code or an unused recovery code.">
              <Input value={code} onChange={(e) => setCode(e.target.value)} required className="font-mono" />
            </Field>
            <Button type="submit" variant="danger" disabled={busy}>Disable two-factor</Button>
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
      <form onSubmit={submit} className="grid gap-4 md:grid-cols-3">
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
  const SCOPES = ["read", "deploy", "cron", "notify", "logs", "db", "containers"];
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
      <form onSubmit={create} className="mt-3 grid gap-3 border-t border-border pt-3 sm:grid-cols-[1fr_auto_auto]">
        <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="CI deploys" required /></Field>
        <Field label="Expires"><select value={ttl} onChange={(e) => setTtl(+e.target.value)} className="h-9 rounded-md border border-border-strong bg-bg px-2 text-sm"><option value={0}>Never</option><option value={30}>30 days</option><option value={90}>90 days</option><option value={365}>1 year</option></select></Field>
        <div className="flex items-end"><Button type="submit" className="h-9">Create token</Button></div>
        <div className="sm:col-span-3"><span className="mb-1 block text-sm font-medium">Scopes</span><div className="flex flex-wrap gap-1"><button type="button" onClick={() => setScopes([])} className={`rounded-sm border px-2 py-0.5 text-xs ${scopes.length === 0 ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>everything</button>{SCOPES.map((sc) => <button key={sc} type="button" onClick={() => setScopes(scopes.includes(sc) ? scopes.filter((x) => x !== sc) : [...scopes, sc])} className={`rounded-sm border px-2 py-0.5 text-xs ${scopes.includes(sc) ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{sc}</button>)}</div></div>
        {msg && <p className="text-sm text-danger sm:col-span-3">{msg}</p>}
      </form>
    </Card>
  );
}

function Users({ meId }: { meId: string }) {
  const [list, setList] = useState<User[]>([]);
  const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [role, setRole] = useState("deployer");
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => api.users().then(setList).catch(() => setList([]));
  useEffect(() => { void load(); }, []);
  const create = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await api.userCreate({ username, password, role }); setUsername(""); setPassword(""); setMsg(`Created ${username}. Share the password over a safe channel; they can change it and enable 2FA in Settings.`); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const setRoleFor = async (u: User, r: string) => { try { await api.userUpdate(u.id, { role: r, password: "" }); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const resetPw = async (u: User) => { const pw = prompt(`New password for ${u.username} (at least 12 characters). Their sessions are signed out.`); if (!pw) return; try { await api.userUpdate(u.id, { role: "", password: pw }); setMsg(`Password for ${u.username} changed.`); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  const remove = async (u: User) => { if (!confirm(`Delete ${u.username}? Their sessions and API tokens are revoked.`)) return; try { await api.userDelete(u.id); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } };
  return (
    <Card title="Users" description="Admins do everything. Deployers can deploy, run jobs and manage containers but not change users, secrets or the host. Viewers only read.">
      <ul className="divide-y divide-border">
        {list.map((u) => (
          <li key={u.id} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
            <div><span className="font-medium">{u.username}</span>{u.id === meId && <span className="ml-1 text-xs text-ink-muted">(you)</span>}<div className="text-xs text-ink-muted">{u.totpEnabled ? "2FA on" : "2FA off"} · {u.lastLoginAt ? `last login ${new Date(u.lastLoginAt).toLocaleString()}` : "never logged in"}</div></div>
            <div className="flex items-center gap-2 text-xs">
              <select value={u.role} onChange={(e) => void setRoleFor(u, e.target.value)} disabled={u.id === meId} className="h-8 rounded-md border border-border-strong bg-bg px-2 text-xs"><option value="admin">admin</option><option value="deployer">deployer</option><option value="viewer">viewer</option></select>
              <button type="button" onClick={() => void resetPw(u)} className="text-ink-muted hover:text-ink">Reset password</button>
              {u.id !== meId && <button type="button" onClick={() => void remove(u)} className="text-danger hover:underline">Delete</button>}
            </div>
          </li>
        ))}
      </ul>
      <form onSubmit={create} className="mt-3 grid gap-2 border-t border-border pt-3 sm:grid-cols-[1fr_1fr_auto_auto]">
        <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="username" required autoComplete="off" />
        <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="password (12+ characters)" required autoComplete="new-password" />
        <select value={role} onChange={(e) => setRole(e.target.value)} className="h-9 rounded-md border border-border-strong bg-bg px-2 text-sm"><option value="admin">admin</option><option value="deployer">deployer</option><option value="viewer">viewer</option></select>
        <Button type="submit" className="h-9">Add user</Button>
        {msg && <p className="text-xs text-ink-muted sm:col-span-4">{msg}</p>}
      </form>
    </Card>
  );
}

function GitHubApp() {
  const [st, setSt] = useState<GitHubState | null>(null);
  const [form, setForm] = useState({ appId: "", clientId: "", slug: "", privateKey: "", webhookSecret: "" });
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const load = () => api.github().then((s) => { setSt(s); setForm((f) => ({ ...f, appId: s.config.appId, clientId: s.config.clientId, slug: s.config.slug })); }).catch(() => {});
  useEffect(() => { void load(); }, []);
  const save = async (e: FormEvent) => { e.preventDefault(); setBusy(true); setMsg(null); try { await api.githubSave(form); setForm((f) => ({ ...f, privateKey: "", webhookSecret: "" })); setMsg("Saved and verified with GitHub."); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } finally { setBusy(false); } };
  const clear = async () => { if (!confirm("Remove the GitHub App credentials? Apps and runners fall back to tokens.")) return; await api.githubSave({ appId: "", clientId: "", slug: "", privateKey: "", webhookSecret: "" }); await load(); };
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
      <form onSubmit={save} className="grid gap-3 sm:grid-cols-3">
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
