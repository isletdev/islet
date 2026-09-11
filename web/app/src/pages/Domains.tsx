import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, RequestError, type Container, type Domain, type ProxyStatus } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";

const EMPTY: Domain = { id: "", host: "", targetType: "container", target: "", port: 80, pathPrefix: "", tls: "letsencrypt", redirectWww: false, basicAuth: "", ipAllowlist: "", rateLimit: 0, headers: "", maintenance: false, protect: false, enabled: true, createdAt: "", updatedAt: "" };

export default function Domains() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [status, setStatus] = useState<ProxyStatus | null>(null);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [certs, setCerts] = useState<{ domain: string; notAfter: string; issuer: string }[]>([]);
  const [editing, setEditing] = useState<Domain | null>(null);
  const [dns, setDns] = useState<Record<string, { ok: boolean; suggestion: string; expected: string }>>({});
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [importing, setImporting] = useState(false);
  const [providers, setProviders] = useState<Record<string, string[]>>({});
  const [dnsProvider, setDnsProvider] = useState("");
  const [dnsEnv, setDnsEnv] = useState<Record<string, string>>({});
  useEffect(() => { void api.dnsProviders().then(setProviders).catch(() => {}); }, []);

  const load = useCallback(async () => {
    try {
      const [st, ds, cs, certList] = await Promise.all([api.proxyStatus(), api.domains(), api.containers().catch(() => []), api.proxyCerts().catch(() => [])]);
      setStatus(st); setDomains(ds); setContainers(cs); setCerts(certList); setErr(null);
      if (!email && st.acmeEmail) setEmail(st.acmeEmail);
      if (st.dnsProvider !== undefined) setDnsProvider((p) => p || st.dnsProvider || "");
    } catch (e) { setErr(e instanceof RequestError ? e.message : String(e)); }
  }, [email]);
  useEffect(() => { void load(); }, [load]);

  const checkDns = async (d: Domain) => {
    try { const r = await api.domainDns(d.id); setDns((m) => ({ ...m, [d.id]: r })); } catch { /* ignore */ }
  };
  useEffect(() => { domains.forEach((d) => void checkDns(d)); }, [domains]);

  const install = async () => {
    setBusy(true); setMsg(null);
    try { await api.proxyInstall(email, { dnsProvider, dnsEnv }); setDnsEnv({}); setMsg("Proxy is running."); await load(); } catch (e) { setMsg(e instanceof RequestError ? e.message : String(e)); }
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
    if (!confirm(`Remove ${d.host}? The route disappears; the container keeps running.`)) return;
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
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Let's Encrypt email" hint="Used for certificate expiry notices. Required for public certificates.">
            <Input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" type="email" className="w-72" />
          </Field>
          {isAdmin && <Button onClick={() => void install()} disabled={busy}>{status?.installed ? "Reinstall / apply" : "Install proxy"}</Button>}
          {isAdmin && status?.installed && <Button variant="secondary" onClick={() => api.proxyRemove().then(load)}>Remove</Button>}
          {msg && <span className="text-sm text-ink-muted">{msg}</span>}
        </div>
        <div className="mt-3 flex flex-wrap items-end gap-3 border-t border-border pt-3">
          <Field label="DNS provider for wildcards" hint="Optional. Lets *.example.com get a certificate through DNS-01."><select value={dnsProvider} onChange={(e) => setDnsProvider(e.target.value)} className="h-9 w-56 rounded-md border border-border-strong bg-bg px-2 text-sm"><option value="">None (HTTP-01 only)</option>{Object.keys(providers).sort().map((p) => <option key={p} value={p}>{p}</option>)}</select></Field>
          {dnsProvider && (providers[dnsProvider] ?? []).map((k) => <Field key={k} label={k} hint={status?.dnsProvider === dnsProvider ? "Leave empty to keep the stored value." : undefined}><Input type="password" value={dnsEnv[k] ?? ""} onChange={(e) => setDnsEnv({ ...dnsEnv, [k]: e.target.value })} autoComplete="off" className="w-56 font-mono" /></Field>)}
        </div>
        <p className="mt-3 text-xs text-ink-muted">Ports 80 and 443 belong to the proxy. Apps are reached by domain, not by published ports. Press "Reinstall / apply" after changing the DNS provider.</p>
      </Card>

      {importing && <NginxImport onDone={async () => { setImporting(false); await load(); }} />}

      <div className="rounded-lg border border-border bg-surface">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <span className="font-semibold">Routed domains</span>
          {isAdmin && <Button variant="secondary" className="h-8 text-xs" onClick={() => setImporting(!importing)}>Import from nginx</Button>}
          {isAdmin && <Button className="h-8 text-xs" onClick={() => { setEditing({ ...EMPTY }); setMsg(null); }}>Add domain</Button>}
        </div>
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-ink-muted"><tr><th className="px-4 py-2 font-medium">Host</th><th className="py-2 font-medium">Target</th><th className="py-2 font-medium">DNS</th><th className="py-2 font-medium">Certificate</th><th className="py-2 pr-4 text-right"></th></tr></thead>
          <tbody className="divide-y divide-border">
            {domains.map((d) => {
              const c = dns[d.id]; const cert = certFor(d.host);
              return (
                <tr key={d.id} className={d.enabled ? "" : "opacity-60"}>
                  <td className="px-4 py-2"><a href={`https://${d.host}`} target="_blank" rel="noreferrer" className="font-medium hover:underline">{d.host}</a>{d.pathPrefix && <span className="ml-1 font-mono text-xs text-ink-muted">{d.pathPrefix}</span>}{d.maintenance && <span className="ml-2 rounded-sm bg-warning-soft px-1.5 py-0.5 text-[10px] text-warning">maintenance</span>}{d.protect && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">login required</span>}{!d.enabled && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">disabled</span>}</td>
                  <td className="py-2 font-mono text-xs text-ink-muted">{d.targetType === "container" ? `${d.target}:${d.port}` : d.targetType === "panel" ? "Islet panel" : d.target}</td>
                  <td className="py-2 text-xs"><span className={`inline-flex items-center gap-1.5 ${c ? (c.ok ? "text-success" : "text-warning") : "text-ink-muted"}`}><span className={`h-1.5 w-1.5 rounded-full ${c ? (c.ok ? "bg-success" : "bg-warning") : "bg-ink-faint"}`} />{c ? (c.ok ? "Points here" : "Not yet") : "Checking…"}</span>{c && !c.ok && <div className="mt-0.5 max-w-[32ch] text-ink-muted">{c.suggestion}</div>}</td>
                  <td className="py-2 text-xs">{d.tls === "none" ? <span className="text-ink-muted">HTTP only</span> : cert ? <span className={new Date(cert.notAfter).getTime() - Date.now() < 14 * 864e5 ? "text-warning" : "text-success"}>valid until {new Date(cert.notAfter).toLocaleDateString()}</span> : d.tls === "self" ? <span className="text-ink-muted">self-signed</span> : <span className="text-ink-muted">pending issue</span>}</td>
                  <td className="py-2 pr-4 text-right whitespace-nowrap">
                    <button type="button" onClick={() => void checkDns(d)} className="text-xs text-ink-muted hover:text-ink">Recheck</button>
                    {isAdmin && <><button type="button" onClick={() => setEditing({ ...d, basicAuth: "" })} className="ml-3 text-xs text-ink-muted hover:text-ink">Edit</button><button type="button" onClick={() => void remove(d)} className="ml-3 text-xs text-danger hover:underline">Remove</button></>}
                  </td>
                </tr>
              );
            })}
            {domains.length === 0 && <tr><td colSpan={5} className="px-4 py-6 text-center text-ink-muted">No domains yet.</td></tr>}
          </tbody>
        </table>
      </div>

      {editing && (
        <Card title={editing.id ? `Edit ${editing.host}` : "Add domain"} description="Create the DNS A record first; the helper checks it for you.">
          <form onSubmit={save} className="grid gap-4 md:grid-cols-2">
            <Field label="Host" hint="e.g. app.example.com. Wildcards need a DNS provider (later)."><div className="flex gap-2"><Input value={editing.host} onChange={(e) => setEditing({ ...editing, host: e.target.value })} required placeholder="app.example.com" /><Button type="button" variant="secondary" onClick={() => void suggest()}>Preview host</Button></div></Field>
            <Field label="Target">
              <select value={editing.targetType} onChange={(e) => setEditing({ ...editing, targetType: e.target.value as Domain["targetType"] })} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm">
                <option value="container">Container</option><option value="panel">Islet panel</option><option value="url">Any URL</option>
              </select>
            </Field>
            {editing.targetType === "container" && <>
              <Field label="Container"><select value={editing.target} onChange={(e) => setEditing({ ...editing, target: e.target.value })} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm" required><option value="">Choose…</option>{containers.map((c) => <option key={c.id} value={c.name}>{c.name} ({c.state})</option>)}</select></Field>
              <Field label="Container port" hint="The port the app listens on inside the container, not a published port."><Input value={String(editing.port)} onChange={(e) => setEditing({ ...editing, port: Number(e.target.value) })} inputMode="numeric" required /></Field>
            </>}
            {editing.targetType === "url" && <Field label="URL"><Input value={editing.target} onChange={(e) => setEditing({ ...editing, target: e.target.value })} placeholder="http://10.0.0.5:8080" required /></Field>}
            <Field label="Certificate">
              <select value={editing.tls} onChange={(e) => setEditing({ ...editing, tls: e.target.value as Domain["tls"] })} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm">
                <option value="letsencrypt">Let's Encrypt (public)</option><option value="self">Self-signed (previews, sslip.io)</option><option value="none">HTTP only</option>
              </select>
            </Field>
            <Field label="Path prefix (optional)" hint="Route only this path, e.g. /api"><Input value={editing.pathPrefix} onChange={(e) => setEditing({ ...editing, pathPrefix: e.target.value })} placeholder="/" /></Field>
            <Field label="Basic auth (optional)" hint="user:password per line. Passwords are hashed on save."><textarea value={editing.basicAuth} onChange={(e) => setEditing({ ...editing, basicAuth: e.target.value })} rows={2} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" /></Field>
            <Field label="IP allowlist (optional)" hint="CIDRs, comma separated. Everyone else gets 403."><Input value={editing.ipAllowlist} onChange={(e) => setEditing({ ...editing, ipAllowlist: e.target.value })} placeholder="203.0.113.7/32, 10.0.0.0/8" /></Field>
            <Field label="Rate limit (req/s, 0 = off)"><Input value={String(editing.rateLimit)} onChange={(e) => setEditing({ ...editing, rateLimit: Number(e.target.value) || 0 })} inputMode="numeric" /></Field>
            <Field label="Extra response headers" hint="Name: value per line"><textarea value={editing.headers} onChange={(e) => setEditing({ ...editing, headers: e.target.value })} rows={2} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" /></Field>
            <div className="flex flex-wrap gap-4 text-sm md:col-span-2">
              <label className="flex items-center gap-1.5"><input type="checkbox" checked={editing.redirectWww} onChange={(e) => setEditing({ ...editing, redirectWww: e.target.checked })} />Redirect www to this host</label>
              <label className="flex items-center gap-1.5"><input type="checkbox" checked={editing.maintenance} onChange={(e) => setEditing({ ...editing, maintenance: e.target.checked })} />Maintenance page</label>
              <label className="flex items-center gap-1.5" title="Visitors must be signed in to the Islet panel. Set the session cookie domain in Settings first."><input type="checkbox" checked={!!editing.protect} onChange={(e) => setEditing({ ...editing, protect: e.target.checked })} />Protect with Islet login</label>
              <label className="flex items-center gap-1.5"><input type="checkbox" checked={editing.enabled} onChange={(e) => setEditing({ ...editing, enabled: e.target.checked })} />Enabled</label>
            </div>
            <div className="flex items-center gap-2 md:col-span-2"><Button type="submit" disabled={busy}>Save</Button><Button type="button" variant="secondary" onClick={() => setEditing(null)}>Cancel</Button>{msg && <span className="text-sm text-ink-muted">{msg}</span>}</div>
          </form>
        </Card>
      )}
    </div>
  );
}

function NginxImport({ onDone }: { onDone: () => Promise<void> }) {
  const [text, setText] = useState("");
  const [found, setFound] = useState<{ host: string; target: string; note: string; saved: boolean }[] | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const preview = async () => { setMsg(null); try { const r = await api.nginxImport(text, false); setFound(r); if (r.length === 0) setMsg(text ? "No server blocks with a server_name found." : "No nginx sites found under /etc/nginx; paste a config below."); } catch (e) { setMsg(e instanceof RequestError ? e.message : String(e)); } };
  const save = async () => { const r = await api.nginxImport(text, true); setFound(r); setMsg(`${r.filter((x) => x.saved).length} domain(s) added. Stop nginx (or move it off ports 80 and 443) so Traefik can answer.`); await onDone(); };
  return (
    <Card title="Import from nginx" description="Reads sites-enabled and conf.d, or paste a config. Proxied sites become domains pointing at the same upstream; static roots are listed so you can deploy them as apps.">
      <textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder="server { server_name app.example.com; location / { proxy_pass http://127.0.0.1:3000; } }" />
      <div className="mt-2 flex items-center gap-2"><Button variant="secondary" className="h-8 text-xs" onClick={() => void preview()}>Preview</Button>{found && found.some((f) => f.target) && <Button className="h-8 text-xs" onClick={() => void save()}>Import proxied sites</Button>}{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>
      {found && found.length > 0 && <ul className="mt-3 divide-y divide-border text-xs">{found.map((f, i) => <li key={i} className="py-1.5"><span className="font-mono">{f.host}</span>{f.target && <span className="ml-2 font-mono text-ink-muted">→ {f.target}</span>}<div className="text-ink-muted">{f.saved ? "added" : f.note}</div></li>)}</ul>}
    </Card>
  );
}
