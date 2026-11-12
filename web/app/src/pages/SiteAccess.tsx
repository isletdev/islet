import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, RequestError, type ProtectedRoute, type SitePerson } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useDialog } from "@/lib/dialogs";
import { copyText } from "@/lib/clipboard";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";

const err = (e: unknown) => (e instanceof RequestError ? e.message : String(e));
const SITE = "site:";

/** Whether a route's allow-list names this person. */
const names = (list: string, username: string) =>
  list.split(/[\s,]+/).map((x) => x.trim().toLowerCase()).includes(SITE + username);

const routeLabel = (r: ProtectedRoute) => r.host + (r.path || "");

/**
 * People who may sign in to a protected site and to nothing else here.
 *
 * They are not panel accounts. They cannot open this panel, its API, a
 * terminal or a file — they have nowhere to type a password except the sign-in
 * page a protected site serves on its own name — which is the point: a client
 * or a colleague can be given one site on a server that holds other people's
 * data, and nothing more.
 */
export default function SiteAccess() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [people, setPeople] = useState<SitePerson[]>([]);
  const [routes, setRoutes] = useState<ProtectedRoute[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);

  const load = useCallback(async () => {
    try { const r = await api.sitePeople(); setPeople(r.people); setRoutes(r.routes); setError(null); }
    catch (e) { setError(err(e)); }
  }, []);
  useEffect(() => { if (isAdmin) void load(); }, [isAdmin, load]);

  if (!isAdmin) {
    return (
      <div className="mx-auto max-w-4xl space-y-6">
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Site access</h1>
        <Alert tone="warning">Only an admin can see and change who may sign in to protected sites.</Alert>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Site access</h1>
          <p className="mt-1 text-ink-muted">
            People who may sign in to a protected site, and to nothing else. They cannot open this panel — they sign in on the
            site&rsquo;s own page, at <span className="font-mono text-[13px]">/_islet/login</span>, and reach only what you tick.
          </p>
        </div>
        {!adding && <Button className="h-8 text-xs" onClick={() => setAdding(true)}>Add person</Button>}
      </div>
      {error && <Alert>{error}</Alert>}
      {routes.length === 0 && (
        <Alert tone="warning">
          No domain asks for a login yet. Set a domain&rsquo;s access to &ldquo;Islet login&rdquo; under <Link to="/domains" className="underline">Domains</Link>,
          then give people access to it here.
        </Alert>
      )}
      {adding && <AddPerson onClose={() => setAdding(false)} onAdded={async (p) => { setAdding(false); await load(); setOpen(p.id); }} />}

      <div className="space-y-3">
        {people.map((p) => {
          const reach = routes.filter((r) => names(r.users, p.username));
          return (
            <div key={p.id} className="rounded-lg border border-border bg-surface">
              <button type="button" onClick={() => setOpen(open === p.id ? null : p.id)} aria-expanded={open === p.id}
                className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-left">
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{p.name || p.username}</span>
                  <span className="block truncate text-xs text-ink-muted">
                    {p.name ? p.username + " · " : ""}{reach.length === 0 ? "no sites yet" : reach.length === 1 ? routeLabel(reach[0]) : `${reach.length} sites`}
                    {p.lastLoginAt ? ` · last signed in ${new Date(p.lastLoginAt).toLocaleString()}` : ""}
                  </span>
                </span>
                <Status p={p} />
              </button>
              {/* Keyed by what they can reach, so a save elsewhere resets the ticks
                  rather than leaving a stale selection to be saved over it. */}
              {open === p.id && <PersonDetail key={reach.map((r) => r.key).join(",")} person={p} routes={routes} onChanged={load} onRemoved={async () => { setOpen(null); await load(); }} />}
            </div>
          );
        })}
        {people.length === 0 && !adding && (
          <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-ink-muted">
            Nobody yet. Add a person, give them a password or an invitation link, and tick the sites they may open.
          </div>
        )}
      </div>
    </div>
  );
}

function Status({ p }: { p: SitePerson }) {
  const chip = (text: string, tone: string) => <span className={`rounded-sm border px-1.5 py-0.5 text-[11px] ${tone}`}>{text}</span>;
  return (
    <span className="flex shrink-0 flex-wrap gap-1">
      {p.disabled && chip("disabled", "border-danger/40 text-danger")}
      {!p.disabled && !p.hasPassword && chip(p.inviteExpires ? "invited" : "no password", "border-warning/40 text-warning")}
      {(p.totpEnabled || p.requireTotp) && chip(p.totpEnabled ? "2FA on" : "2FA at next sign-in", "border-border-strong text-ink-muted")}
    </span>
  );
}

function AddPerson({ onClose, onAdded }: { onClose: () => void; onAdded: (p: SitePerson) => void }) {
  const [username, setUsername] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [requireTotp, setRequireTotp] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setMsg(null);
    try { onAdded(await api.sitePersonCreate({ username, name, password, requireTotp })); }
    catch (er) { setMsg(err(er)); }
    finally { setBusy(false); }
  };
  return (
    <Card title="Add a person" description="They get no panel account. Leave the password empty to send them an invitation link instead, so they choose their own.">
      <form onSubmit={submit} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="Username" hint="What they sign in with. An email address works.">
          <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="anna@client.com" autoCapitalize="none" spellCheck={false} required />
        </Field>
        <Field label="Name" hint="Optional. Only you see it.">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Anna Novak" />
        </Field>
        <Field label="Password" hint="At least 12 characters, or empty for an invitation link.">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
        </Field>
        <Field label="" className="sm:self-end">
          <label className="flex items-start gap-2 py-2 text-sm">
            <input type="checkbox" className="mt-0.5" checked={requireTotp} onChange={(e) => setRequireTotp(e.target.checked)} />
            <span>Require a code from an authenticator app<span className="block text-xs text-ink-muted">They set it up the first time they sign in.</span></span>
          </label>
        </Field>
        {msg && <p className="text-sm text-danger sm:col-span-2">{msg}</p>}
        <div className="flex gap-2 sm:col-span-2">
          <Button type="submit" disabled={busy}>{busy ? "Adding…" : "Add person"}</Button>
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
        </div>
      </form>
    </Card>
  );
}

function PersonDetail({ person: p, routes, onChanged, onRemoved }: { person: SitePerson; routes: ProtectedRoute[]; onChanged: () => Promise<void>; onRemoved: () => Promise<void> }) {
  const ask = useDialog();
  const initial = useMemo(() => routes.filter((r) => names(r.users, p.username)).map((r) => r.key), [routes, p.username]);
  const [picked, setPicked] = useState<string[]>(initial);
  const [password, setPassword] = useState("");
  const [inviteHost, setInviteHost] = useState("");
  const [invite, setInvite] = useState<{ link: string; expiresAt: string } | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  // Hosts a link can be made for: named outright, not a wildcard, which is a
  // pattern rather than an address anybody can open.
  const hosts = useMemo(() => [...new Set(routes.map((r) => r.host).filter((h) => !h.startsWith("*.")))], [routes]);
  const linkHost = inviteHost || hosts.find((h) => routes.some((r) => r.host === h && picked.includes(r.key))) || hosts[0] || "";
  const dirty = picked.length !== initial.length || picked.some((k) => !initial.includes(k));

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true); setMsg(null);
    try { await fn(); await onChanged(); setMsg({ ok: true, text: ok }); }
    catch (e) { setMsg({ ok: false, text: err(e) }); }
    finally { setBusy(false); }
  };

  return (
    <div className="space-y-5 border-t border-border px-4 py-4">
      <section>
        <h3 className="text-sm font-medium">Sites</h3>
        <p className="mt-0.5 text-xs text-ink-muted">What {p.name || p.username} may open after signing in. Panel accounts already allowed on a site keep their access.</p>
        {routes.length === 0
          ? <p className="mt-2 text-sm text-ink-muted">No domain asks for a login yet.</p>
          : (
            <div className="mt-2 grid grid-cols-1 gap-1 sm:grid-cols-2">
              {routes.map((r) => (
                <label key={r.key} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-surface-2">
                  <input type="checkbox" checked={picked.includes(r.key)} onChange={() => setPicked(picked.includes(r.key) ? picked.filter((k) => k !== r.key) : [...picked, r.key])} />
                  <span className="min-w-0 truncate">{r.host}<span className="font-mono text-ink-muted">{r.path || ""}</span>{!r.path && <span className="text-ink-muted"> — whole site</span>}</span>
                </label>
              ))}
            </div>
          )}
        {routes.length > 0 && (
          <Button className="mt-2 h-8 px-3 text-xs" disabled={busy || !dirty} onClick={() => void run(() => api.sitePersonAccess(p.id, picked), "Access saved. The proxy picks it up within a second or two.")}>
            Save sites
          </Button>
        )}
      </section>

      <section>
        <h3 className="text-sm font-medium">Password</h3>
        <div className="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <form className="flex min-w-0 gap-2" onSubmit={(e) => { e.preventDefault(); void run(async () => { await api.sitePersonUpdate(p.id, { password }); setPassword(""); }, "Password set. They are signed out everywhere and use the new one from now on."); }}>
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={p.hasPassword ? "New password" : "Set a password"} autoComplete="new-password" minLength={12} required className="min-w-0" />
            <Button type="submit" variant="secondary" className="shrink-0" disabled={busy}>Set</Button>
          </form>
          <div className="flex min-w-0 gap-2">
            <Select value={linkHost} onChange={(e) => setInviteHost(e.target.value)} className="min-w-0" aria-label="Site for the invitation link" disabled={hosts.length === 0}>
              {hosts.map((h) => <option key={h} value={h}>{h}</option>)}
            </Select>
            <Button type="button" variant="secondary" className="shrink-0" disabled={busy || !linkHost} onClick={() => void run(async () => setInvite(await api.sitePersonInvite(p.id, linkHost)), "Invitation link made. It works once, for 72 hours.")}>
              Invitation link
            </Button>
          </div>
        </div>
        {invite && (
          <div className="mt-2 flex min-w-0 items-center gap-2 rounded-md border border-border bg-surface-2 px-2 py-1.5">
            <code className="min-w-0 flex-1 truncate font-mono text-xs" title={invite.link}>{invite.link}</code>
            <Button type="button" variant="secondary" className="h-7 shrink-0 px-2 text-xs" onClick={() => void copyText(invite.link)}>Copy</Button>
          </div>
        )}
      </section>

      <section>
        <h3 className="text-sm font-medium">Sign-in</h3>
        <div className="mt-2 flex flex-wrap gap-2">
          <Button type="button" variant="secondary" className="h-8 px-3 text-xs" disabled={busy}
            onClick={() => void run(() => api.sitePersonUpdate(p.id, { requireTotp: !p.requireTotp }), p.requireTotp ? "Authenticator code no longer required." : "They set up an authenticator code at their next sign-in.")}>
            {p.requireTotp ? "Stop requiring a code" : "Require an authenticator code"}
          </Button>
          {p.totpEnabled && (
            <Button type="button" variant="secondary" className="h-8 px-3 text-xs" disabled={busy}
              onClick={async () => { if (await ask.confirm({ title: `Reset ${p.username}'s authenticator?`, body: "For a lost phone. They are signed out, and if a code is required they set up a new one at their next sign-in.", confirmLabel: "Reset" })) void run(() => api.sitePersonUpdate(p.id, { resetTotp: true }), "Authenticator reset."); }}>
              Reset authenticator
            </Button>
          )}
          <Button type="button" variant="secondary" className="h-8 px-3 text-xs" disabled={busy}
            onClick={() => void run(() => api.sitePersonUpdate(p.id, { disabled: !p.disabled }), p.disabled ? "They can sign in again." : "Disabled and signed out everywhere.")}>
            {p.disabled ? "Enable" : "Disable"}
          </Button>
          <Button type="button" variant="danger" className="h-8 px-3 text-xs" disabled={busy}
            onClick={async () => {
              if (!(await ask.confirm({ title: `Remove ${p.username}?`, body: "They are signed out and taken off every site. A site that named only them goes back to letting in any Islet account, as an empty list always has.", confirmLabel: "Remove", tone: "danger" }))) return;
              setBusy(true);
              try { await api.sitePersonDelete(p.id); await onRemoved(); } catch (e) { setMsg({ ok: false, text: err(e) }); setBusy(false); }
            }}>
            Remove
          </Button>
        </div>
      </section>
      {msg && <p className={`text-sm ${msg.ok ? "text-success" : "text-danger"}`}>{msg.text}</p>}
    </div>
  );
}
