import { Link } from "react-router-dom";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, RequestError, type Container, type Domain, type DomainLocation, type ProxyStatus , type DNSCheck } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { useDialog } from "@/lib/dialogs";

const EMPTY: Domain = { id: "", host: "", targetType: "container", target: "", port: 80, pathPrefix: "", tls: "letsencrypt", redirectWww: false, basicAuth: "", ipAllowlist: "", rateLimit: 0, headers: "", maintenance: false, protect: false, enabled: true, passHost: true, blockExploits: false, locations: [], createdAt: "", updatedAt: "" };

/**
 * Extra paths on one host, each forwarded somewhere of its own.
 *
 * Nginx Proxy Manager calls these custom locations and nginx calls them
 * location blocks; they are the reason one hostname can put an API on /api and
 * the site itself on /. Everything guarding the host guards them too — the
 * same certificate, login, allowlist and limits — so there is nothing to
 * repeat here beyond the path and where it goes.
 */
function Locations({ value, containers, onChange }: { value: DomainLocation[]; containers: Container[]; onChange: (v: DomainLocation[]) => void }) {
  const set = (i: number, patch: Partial<DomainLocation>) =>
    onChange(value.map((l, n) => (n === i ? { ...l, ...patch } : l)));
  return (
    <div className="rounded-md border border-border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <span className="text-sm font-medium">Custom locations</span>
          <p className="mt-0.5 text-xs text-ink-muted">
            Paths on this host that go somewhere else. Everything above applies to them too:
            the same certificate, login and limits. A longer path wins, so /api/v2 beats /api.
          </p>
        </div>
        <Button
          type="button"
          variant="secondary"
          className="h-8 px-2.5 text-xs"
          onClick={() => onChange([...value, { path: "", targetType: "container", target: "", port: 80, stripPath: false }])}
        >
          Add a location
        </Button>
      </div>
      {value.length > 0 && (
        <ul className="mt-3 space-y-2">
          {value.map((l, i) => (
            <li key={i} className="grid grid-cols-1 gap-2 rounded-md border border-border bg-surface p-2 sm:grid-cols-[8rem_7rem_minmax(0,1fr)_6rem_auto]">
              <Input value={l.path} onChange={(e) => set(i, { path: e.target.value })} placeholder="/api" className="h-8 font-mono text-xs" aria-label="Path" />
              <Select value={l.targetType} onChange={(e) => set(i, { targetType: e.target.value as DomainLocation["targetType"], target: "" })} className="h-8 text-xs" aria-label="Goes to">
                <option value="container">Container</option><option value="url">URL</option><option value="panel">Islet panel</option>
              </Select>
              {l.targetType === "container" && (
                <Select value={l.target} onChange={(e) => set(i, { target: e.target.value })} className="h-8 text-xs" aria-label="Container">
                  <option value="">Choose…</option>
                  {containers.map((c) => <option key={c.id} value={c.name}>{c.name}</option>)}
                </Select>
              )}
              {l.targetType === "url" && (
                <Input value={l.target} onChange={(e) => set(i, { target: e.target.value })} placeholder="http://10.0.0.5:8080" className="h-8 text-xs" aria-label="URL" />
              )}
              {l.targetType === "panel" && <span className="self-center text-xs text-ink-muted">The Islet panel itself.</span>}
              {l.targetType === "container"
                ? <Input value={String(l.port)} onChange={(e) => set(i, { port: Number(e.target.value) || 0 })} inputMode="numeric" className="h-8 text-xs" aria-label="Port" />
                : <span />}
              <button type="button" onClick={() => onChange(value.filter((_, n) => n !== i))} className="self-center px-1 text-xs text-danger hover:underline">Remove</button>
              <label className="col-span-full flex items-center gap-1.5 text-xs text-ink-muted" title="With this on, /api/things reaches the app as /things.">
                <input type="checkbox" checked={l.stripPath} onChange={(e) => set(i, { stripPath: e.target.checked })} />
                Remove {l.path || "the path"} before forwarding
              </label>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Why protecting this host will not work, or "" when it will.
 *
 * Forward auth asks the panel whether the visitor is signed in, and the visitor
 * proves that with the session cookie the browser sent to the protected host.
 * A cookie can only be scoped to a parent of the panel's own name, so a host
 * outside that parent never receives one and the login redirects forever. It
 * is worth saying at the moment the box is ticked rather than discovering it
 * from an infinite loop.
 */
function protectWarning(host: string, cookieDom: string): string {
  const h = host.trim().toLowerCase().replace(/^\*\./, "");
  if (!h) return "";
  if (!cookieDom) {
    return `This needs a session cookie domain, and none is set. Without one the panel's cookie is only sent to ${location.hostname}, so ${h} can never tell that a visitor is signed in and the login will loop. Set one under Settings, Sessions.`;
  }
  if (h === cookieDom || h.endsWith("." + cookieDom)) return "";
  return `${h} is not under ${cookieDom}, which is what the session cookie is scoped to, so a browser will never send it there and the login will loop. An Islet login can only protect names under ${cookieDom} — for ${h} you would need a panel on a name under ${h} instead.`;
}

export default function Domains() {
  const ask = useDialog();
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [status, setStatus] = useState<ProxyStatus | null>(null);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [certs, setCerts] = useState<{ domain: string; notAfter: string; issuer: string }[]>([]);
  const [editing, setEditing] = useState<Domain | null>(null);
  const [dns, setDns] = useState<Record<string, DNSCheck>>({});
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [providers, setProviders] = useState<Record<string, string[]>>({});
  const [dnsProvider, setDnsProvider] = useState("");
  const [dnsEnv, setDnsEnv] = useState<Record<string, string>>({});
  // The form is hidden once there is nothing to decide; this opens it again.
  const [settings, setSettings] = useState(false);
  const [cookieDom, setCookieDom] = useState("");
  useEffect(() => { void api.cookieDomain().then((r) => setCookieDom(r.cookieDomain)).catch(() => {}); }, []);
  // What the last apply did, kept outside the form so closing it does not take
  // the only confirmation with it.
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => { void api.dnsProviders().then(setProviders).catch(() => {}); }, []);

  const load = useCallback(async () => {
    try {
      const [st, ds, cs, certList] = await Promise.all([api.proxyStatus(), api.domains(), api.containers().catch(() => []), api.proxyCerts().catch(() => [])]);
      setStatus(st); setDomains(ds); setContainers(cs); setCerts(certList); setErr(null);
      if (!email && st.acmeEmail) setEmail(st.acmeEmail);
      if (st.dnsProvider !== undefined) setDnsProvider((p) => p || st.dnsProvider || "");
    } catch (e) { setErr(e instanceof RequestError ? e.message : String(e)); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const checkDns = async (d: Domain) => {
    try { const r = await api.domainDns(d.id); setDns((m) => ({ ...m, [d.id]: r })); } catch { /* ignore */ }
  };
  useEffect(() => { domains.forEach((d) => void checkDns(d)); }, [domains]);

  const install = async () => {
    setBusy(true); setMsg(null);
    try {
      const st = await api.proxyInstall(email, { dnsProvider, dnsEnv });
      setDnsEnv({});
      setSettings(false);
      // The note goes where it will still be visible after the form closes.
      setNote(st.acmeEmail
        ? `Applied. Certificates will be requested for ${st.acmeEmail}; it takes a minute.`
        : "Applied.");
      await load();
    } catch (e) {
      setMsg(e instanceof RequestError ? e.message : String(e));
    }
    finally { setBusy(false); }
  };
  const save = async (e: FormEvent) => {
    e.preventDefault(); if (!editing) return;
    setBusy(true); setMsg(null);
    try { await api.domainSave(editing); setEditing(null); setMsg("Saved. The proxy picks it up within a few seconds."); await load(); }
    catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); }
    finally { setBusy(false); }
  };
  const remove = async (d: Domain) => {
    if (!(await ask.confirm({ title: `Stop routing ${d.host}?`, body: "The domain stops resolving to anything here. Whatever it points at keeps running.", confirmLabel: "Remove route", tone: "danger" }))) return;
    try { await api.domainDelete(d.id); await load(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); }
  };
  const suggest = async () => {
    if (!editing) return;
    const name = (editing.targetType === "container" ? editing.target : "app").replace(/[^a-z0-9-]/gi, "-").toLowerCase() || "app";
    try { const r = await api.previewHost(name); if (r.host) setEditing({ ...editing, host: r.host, tls: "self" }); else setMsg("No public IP detected; type a host."); } catch { /* ignore */ }
  };
  const certFor = (host: string) => certs.find((c) => c.domain === host);

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Domains</h1>
        <p className="mt-1 text-ink-muted">Point a domain at a container and get HTTPS. Traefik does the routing; Islet writes its config.</p>
      </div>
      {err && <Alert>{err}</Alert>}

      <Card title="Reverse proxy" description={status?.running ? `Traefik is running on ports ${status.httpPort} and ${status.httpsPort}.` : status?.installed ? "Traefik is installed but not running." : "Not installed. Install it to route domains."}>
        {status?.needsRestart && status.running && (
          <div className="mb-3 rounded-md border border-warning/40 bg-warning-soft p-3 text-sm text-warning">
            <p className="font-medium">The proxy is running with settings from an older version of Islet.</p>
            <p className="mt-1">Your sites are being served and every domain still works. Recreating the container applies the newer settings and takes a moment, during which sites are briefly unreachable, so it waits for you. If the new container does not come up, the current one is put back.</p>
            {isAdmin && <Button className="mt-2 h-8 text-xs" disabled={busy} onClick={() => void install()}>{busy ? "Applying…" : "Apply the new settings"}</Button>}
          </div>
        )}
        {/* Why there are no certificates, in words, from Islet's own
            configuration and from what Traefik has been saying. Without this
            the page showed a running proxy, an empty certificate list, and
            nothing at all connecting the two. */}
        {(status?.problems ?? []).length > 0 && (
          <div className="mb-3 rounded-md border border-warning/40 bg-warning-soft p-3 text-sm text-warning">
            <p className="font-medium">Certificates are not being issued.</p>
            <ul className="mt-1 list-disc space-y-1 pl-5">
              {(status?.problems ?? []).map((p) => <li key={p}>{p}</li>)}
            </ul>
            {isAdmin && !settings && (
              <Button variant="secondary" className="mt-2 h-8 text-xs" onClick={() => setSettings(true)}>
                Open settings
              </Button>
            )}
          </div>
        )}
        {note && !settings && (
          <p className="mb-3 rounded-md border border-border bg-surface-2 p-2 text-sm text-ink-muted">{note}</p>
        )}
        {status?.installed && !settings && (
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
            <Fact label="Ports">{status.httpPort} and {status.httpsPort}</Fact>
            <Fact label="Certificates">
              {certs.length === 0
                ? <span className="text-ink-muted">none yet</span>
                : <>{certs.length}, next renewal {renewalDate(certs)}</>}
            </Fact>
            <Fact label="Let's Encrypt account">
              {status.acmeEmail || <span className="font-medium text-warning">no email — no certificates</span>}
            </Fact>
            <Fact label="Wildcards">
              {status.dnsProvider || <span className="text-ink-muted">off, HTTP-01 only</span>}
            </Fact>
            {isAdmin && (
              <Button variant="secondary" className="ml-auto h-8 px-2.5 text-xs" onClick={() => setSettings(true)}>
                Settings
              </Button>
            )}
          </div>
        )}

        {(!status?.installed || settings) && (
          <>
            <div className="flex flex-wrap items-start gap-3">
              <Field
                label="Let's Encrypt email"
                hint="Without one there is no Let's Encrypt account, so no public certificate can be issued for any domain here."
              >
                <Input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" type="email" className="w-72" required />
              </Field>
              <Field label="Wildcard certificates" className="w-56" hint="Lets *.example.com get a certificate over DNS-01.">
                <Select value={dnsProvider} onChange={(e) => setDnsProvider(e.target.value)}>
                  <option value="">Off — HTTP-01 only</option>
                  {Object.keys(providers).sort().map((p) => <option key={p} value={p}>{p}</option>)}
                </Select>
              </Field>
              {dnsProvider && (providers[dnsProvider] ?? []).map((k) => (
                <Field key={k} label={k} className="w-56" hint={status?.dnsProvider === dnsProvider ? "Leave empty to keep the stored value." : undefined}>
                  <Input type="password" value={dnsEnv[k] ?? ""} onChange={(e) => setDnsEnv({ ...dnsEnv, [k]: e.target.value })} autoComplete="off" className="font-mono" />
                </Field>
              ))}
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              {isAdmin && <Button onClick={() => void install()} disabled={busy}>{status?.installed ? "Apply" : "Install the proxy"}</Button>}
              {settings && <Button variant="secondary" onClick={() => setSettings(false)}>Cancel</Button>}
              {isAdmin && status?.installed && <Button variant="danger" className="ml-auto" onClick={() => api.proxyRemove().then(load)}>Remove the proxy</Button>}
              {msg && <span className="text-sm text-ink-muted">{msg}</span>}
            </div>
            <p className="mt-3 text-xs text-ink-muted">
              Ports 80 and 443 belong to the proxy; apps are reached by domain rather than by a published port. Certificates
              renew on their own, about thirty days before they expire. Changing the wildcard provider takes effect when you
              press Apply.
            </p>
          </>
        )}
      </Card>


      <div className="rounded-lg border border-border bg-surface">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <span className="font-semibold">Routed domains</span>
          {isAdmin && <Link to="/domains/import" className="inline-flex h-8 items-center rounded-md border border-border px-2.5 text-xs text-ink hover:bg-surface-2">Import existing sites</Link>}
          {isAdmin && <Button className="h-8 text-xs" onClick={() => { setEditing({ ...EMPTY }); setMsg(null); }}>Add domain</Button>}
        </div>
        <div className="overflow-x-auto"><table className="w-full min-w-[640px] text-sm">
          <thead className="text-left text-xs text-ink-muted"><tr><th className="px-4 py-2 font-medium">Host</th><th className="py-2 font-medium">Target</th><th className="py-2 font-medium">DNS</th><th className="py-2 font-medium">Certificate</th><th className="py-2 pr-4 text-right"></th></tr></thead>
          <tbody className="divide-y divide-border">
            {domains.map((d) => {
              const c = dns[d.id]; const cert = certFor(d.host);
              return (
                <tr key={d.id} className={d.enabled ? "" : "opacity-60"}>
                  <td className="px-4 py-2"><a href={`https://${d.host}`} target="_blank" rel="noreferrer" className="-my-1 inline-block py-1 font-medium hover:underline">{d.host}</a>{d.pathPrefix && <span className="ml-1 font-mono text-xs text-ink-muted">{d.pathPrefix}</span>}{d.maintenance && <span className="ml-2 rounded-sm bg-warning-soft px-1.5 py-0.5 text-[10px] text-warning">maintenance</span>}{d.protect && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">login required</span>}{!d.enabled && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">disabled</span>}</td>
                  <td className="py-2 font-mono text-xs text-ink-muted">{d.targetType === "container" ? `${d.target}:${d.port}` : d.targetType === "panel" ? "Islet panel" : d.target}</td>
                  <td className="py-2 text-xs"><DnsCell check={c} /></td>
                  <td className="py-2 text-xs">{d.tls === "none" ? <span className="text-ink-muted">HTTP only</span> : cert ? <span className={new Date(cert.notAfter).getTime() - Date.now() < 14 * 864e5 ? "text-warning" : "text-success"}>valid until {new Date(cert.notAfter).toLocaleDateString()}</span> : d.tls === "self" ? <span className="text-ink-muted">self-signed</span> : <span className="text-ink-muted">pending issue</span>}</td>
                  <td className="py-2 pr-4 text-right whitespace-nowrap">
                    <button type="button" onClick={() => void checkDns(d)} className="-my-1 py-1 text-xs text-ink-muted hover:text-ink">Recheck</button>
                    {isAdmin && <><button type="button" onClick={() => setEditing({ ...d, basicAuth: "" })} className="-my-1 ml-3 py-1 text-xs text-ink-muted hover:text-ink">Edit</button><button type="button" onClick={() => void remove(d)} className="-my-1 ml-3 py-1 text-xs text-danger hover:underline">Remove</button></>}
                  </td>
                </tr>
              );
            })}
            {domains.length === 0 && <tr><td colSpan={5} className="px-4 py-6 text-center text-ink-muted">No domains yet. Point an A record at this server, add the host here and pick what it should reach: a container, the panel, or any URL. HTTPS is automatic.</td></tr>}
          </tbody>
        </table></div>
      </div>

      {editing && (() => {
        // A wildcard is decided by the host being typed, not by what was
        // saved, so the certificate field answers while the person types.
        const editingWildcard = editing.host.trim().startsWith("*.");
        const savedProvider = status?.dnsProvider ?? "";
        return (
        <Card title={editing.id ? `Edit ${editing.host}` : "Add domain"} description="Create the DNS A record first; the helper checks it for you.">
          {editingWildcard && !savedProvider && (
            <Alert>
              A wildcard certificate is issued by proving control of the DNS zone, so it needs a DNS
              provider. Set one under Settings above, or give this host a self-signed certificate.
            </Alert>
          )}
          <form onSubmit={save} className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <Field label="Host" hint="e.g. app.example.com, or *.example.com for every name in the zone."><div className="flex gap-2"><Input value={editing.host} onChange={(e) => setEditing({ ...editing, host: e.target.value })} required placeholder="app.example.com" /><Button type="button" variant="secondary" onClick={() => void suggest()}>Preview host</Button></div></Field>
            <Field label="Target">
              <Select value={editing.targetType} onChange={(e) => setEditing({ ...editing, targetType: e.target.value as Domain["targetType"] })}>
                <option value="container">Container</option><option value="panel">Islet panel</option><option value="url">Any URL</option>
              </Select>
            </Field>
            {editing.targetType === "container" && <>
              <Field label="Container"><Select value={editing.target} onChange={(e) => setEditing({ ...editing, target: e.target.value })} required><option value="">Choose…</option>{containers.map((c) => <option key={c.id} value={c.name}>{c.name} ({c.state})</option>)}</Select></Field>
              <Field label="Container port" hint="The port the app listens on inside the container, not a published port."><Input value={String(editing.port)} onChange={(e) => setEditing({ ...editing, port: Number(e.target.value) })} inputMode="numeric" required /></Field>
            </>}
            {editing.targetType === "url" && <Field label="URL"><Input value={editing.target} onChange={(e) => setEditing({ ...editing, target: e.target.value })} placeholder="http://10.0.0.5:8080" required /></Field>}
            <Field
              label="Certificate"
              hint={
                editingWildcard
                  ? "A wildcard can only be issued over DNS: there is no single name to answer an HTTP challenge on."
                  : editing.tls === "letsencrypt-dns"
                    ? savedProvider
                      ? `Proved through ${savedProvider}, so port 80 does not have to reach this server. This is the one to use behind Cloudflare or on a private network.`
                      : "No DNS provider is set yet — add one under Settings above, or issue this certificate over HTTP."
                    : "Proved over HTTP on port 80, which has to reach this server from the internet."
              }
            >
              <Select value={editing.tls} onChange={(e) => setEditing({ ...editing, tls: e.target.value as Domain["tls"] })}>
                {/* A wildcard has no HTTP-01 option at all: there is no single
                    name to answer a challenge on. */}
                {!editingWildcard && <option value="letsencrypt">Let's Encrypt (HTTP challenge)</option>}
                <option value="letsencrypt-dns">Let's Encrypt (DNS challenge){savedProvider ? ` · ${savedProvider}` : " · needs a provider"}</option>
                <option value="self">Self-signed (previews, sslip.io)</option>
                <option value="none">HTTP only</option>
              </Select>
            </Field>
            <Field label="Path prefix (optional)" hint="Serve only this path on this host, e.g. /api. Leave empty for the whole host."><Input value={editing.pathPrefix} onChange={(e) => setEditing({ ...editing, pathPrefix: e.target.value })} placeholder="/" /></Field>
            <div className="md:col-span-2">
              <Locations
                value={editing.locations ?? []}
                containers={containers}
                onChange={(locations) => setEditing({ ...editing, locations })}
              />
            </div>
            <Field label="Basic auth (optional)" hint="user:password per line. Passwords are hashed on save."><textarea value={editing.basicAuth} onChange={(e) => setEditing({ ...editing, basicAuth: e.target.value })} rows={2} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" /></Field>
            <Field label="IP allowlist (optional)" hint="CIDRs, comma separated. Everyone else gets 403."><Input value={editing.ipAllowlist} onChange={(e) => setEditing({ ...editing, ipAllowlist: e.target.value })} placeholder="203.0.113.7/32, 10.0.0.0/8" /></Field>
            <Field label="Rate limit (req/s, 0 = off)"><Input value={String(editing.rateLimit)} onChange={(e) => setEditing({ ...editing, rateLimit: Number(e.target.value) || 0 })} inputMode="numeric" /></Field>
            <Field label="Extra response headers" hint="Name: value per line"><textarea value={editing.headers} onChange={(e) => setEditing({ ...editing, headers: e.target.value })} rows={2} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" /></Field>
            <div className="flex flex-wrap gap-4 text-sm md:col-span-2">
              <label className="flex items-center gap-1.5"><input type="checkbox" checked={editing.redirectWww} onChange={(e) => setEditing({ ...editing, redirectWww: e.target.checked })} />Redirect www to this host</label>
              <label className="flex items-center gap-1.5"><input type="checkbox" checked={editing.maintenance} onChange={(e) => setEditing({ ...editing, maintenance: e.target.checked })} />Maintenance page</label>
              <label className="flex items-center gap-1.5" title="Visitors must be signed in to the Islet panel. Set the session cookie domain in Settings first."><input type="checkbox" checked={!!editing.protect} onChange={(e) => setEditing({ ...editing, protect: e.target.checked })} />Protect with Islet login</label>
              <label className="flex items-center gap-1.5" title="Scanners look for .env, .git and similar within seconds of a new name appearing. This refuses them.">
                <input type="checkbox" checked={editing.blockExploits ?? false} onChange={(e) => setEditing({ ...editing, blockExploits: e.target.checked })} />
                Block common exploits
              </label>
              <label className="flex items-center gap-1.5" title="On: the app is told the hostname the visitor typed, which is what nginx does and what WebSocket and login checks expect. Off: it is told the upstream's own address.">
                <input type="checkbox" checked={editing.passHost ?? true} onChange={(e) => setEditing({ ...editing, passHost: e.target.checked })} />
                Send this hostname to the app
              </label>
              <label className="flex items-center gap-1.5"><input type="checkbox" checked={editing.enabled} onChange={(e) => setEditing({ ...editing, enabled: e.target.checked })} />Enabled</label>
            </div>
            {editing.protect && protectWarning(editing.host, cookieDom) && (
              <div className="md:col-span-2">
                <Alert tone="warning">{protectWarning(editing.host, cookieDom)}</Alert>
              </div>
            )}
            <div className="flex items-center gap-2 md:col-span-2"><Button type="submit" disabled={busy}>Save</Button><Button type="button" variant="secondary" onClick={() => setEditing(null)}>Cancel</Button>{msg && <span className="text-sm text-ink-muted">{msg}</span>}</div>
          </form>
        </Card>
        );
      })()}
    </div>
  );
}

/**
 * What DNS says about a host.
 *
 * Three outcomes, not two. A domain behind Cloudflare resolves to Cloudflare
 * and not to this server, which is the proxy doing its job; calling that "not
 * yet" and telling the person to change their A record is telling them to
 * switch off the thing they deliberately switched on.
 */
function DnsCell({ check }: { check?: DNSCheck }) {
  if (!check) {
    return (
      <span className="inline-flex items-center gap-1.5 text-ink-muted">
        <span className="h-1.5 w-1.5 rounded-full bg-ink-faint" />Checking…
      </span>
    );
  }
  const state = check.ok
    ? { dot: "bg-success", text: "text-success", label: "Points here" }
    : check.proxiedBy
      ? { dot: "bg-accent", text: "text-accent", label: `Behind ${check.proxiedBy}` }
      : { dot: "bg-warning", text: "text-warning", label: "Not yet" };
  return (
    <>
      <span className={`inline-flex items-center gap-1.5 ${state.text}`}>
        <span className={`h-1.5 w-1.5 rounded-full ${state.dot}`} />{state.label}
      </span>
      {!check.ok && <div className="mt-0.5 max-w-[32ch] text-ink-muted">{check.suggestion}</div>}
    </>
  );
}


/** One fact on the proxy card: a label above the thing it names. */
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] text-ink-faint">{label}</div>
      <div className="text-sm">{children}</div>
    </div>
  );
}

/**
 * When the next certificate renews.
 *
 * Traefik renews about thirty days before expiry, so the earliest expiry minus
 * thirty days is the next time anything happens. Saying so is the answer to
 * "do these renew by themselves", asked in front of the evidence.
 */
function renewalDate(certs: { notAfter: string }[]): string {
  const soonest = certs
    .map((c) => new Date(c.notAfter).getTime())
    .filter((t) => Number.isFinite(t))
    .sort((a, b) => a - b)[0];
  if (!soonest) return "unknown";
  return new Date(soonest - 30 * 864e5).toLocaleDateString();
}
