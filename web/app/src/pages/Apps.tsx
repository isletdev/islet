import { lazy, Suspense, useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";

const Deploys = lazy(() => import("./Deploys"));
const RecipesPage = lazy(() => import("./Recipes"));
import { api, RequestError, type CatalogApp, type InstalledApp } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { postStream } from "@/lib/stream";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import AppIcon from "@/components/AppIcon";

export default function Apps() {
  const { state } = useAuth();
  const canInstall = state.status === "authed" && state.me.user.role !== "viewer";
  const [apps, setApps] = useState<CatalogApp[]>([]);
  const [installed, setInstalled] = useState<InstalledApp[]>([]);
  const [q, setQ] = useState("");
  const [params, setParams] = useSearchParams();
  const [cat, setCat] = useState(params.get("category") ?? "all");
  const [sel, setSel] = useState<CatalogApp | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [updates, setUpdates] = useState<Record<string, string[]>>({});
  const [updating, setUpdating] = useState<string | null>(null);
  const tab = params.get("tab") === "catalog" ? "catalog" : params.get("tab") === "recipes" ? "recipes" : "deploys";
  const pickCategory = (c: string) => {
    setCat(c);
    const next: Record<string, string> = { tab: "catalog" };
    if (c !== "all") next.category = c;
    setParams(next, { replace: true });
  };

  const load = () => Promise.all([api.catalog(), api.installedApps()]).then(([a, i]) => { setApps(a); setInstalled(i); }).catch((e) => setErr(e instanceof RequestError ? e.message : String(e)));
  useEffect(() => { void load(); void api.installedUpdates().then(setUpdates).catch(() => {}); }, []);

  const cats = useMemo(() => ["all", ...Array.from(new Set(apps.map((a) => a.category))).sort()], [apps]);
  const shown = apps.filter((a) => (cat === "all" || a.category === cat) && (!q || `${a.name} ${a.description}`.toLowerCase().includes(q.toLowerCase())));

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Apps</h1>
        <p className="mt-1 text-ink-muted">Deploy your own code from Git, or install one-click apps from the catalog.</p>
      </div>
      <div className="flex gap-1 border-b border-border text-sm">
        {(["deploys", "catalog", "recipes"] as const).map((t) => <button key={t} type="button" onClick={() => setParams(t === "deploys" ? {} : { tab: t })} className={`-mb-px border-b-2 px-3 py-2 ${tab === t ? "border-ink font-medium" : "border-transparent text-ink-muted hover:text-ink"}`}>{t === "deploys" ? "Your apps" : t === "catalog" ? "Catalog" : "Recipes"}</button>)}
      </div>
      {tab === "deploys" && <Suspense fallback={<p className="text-sm text-ink-muted">Loading…</p>}><Deploys /></Suspense>}
      {tab === "recipes" && <Suspense fallback={<p className="text-sm text-ink-muted">Loading…</p>}><RecipesPage /></Suspense>}
      {tab === "catalog" && <>
      {err && <Alert>{err}</Alert>}

      {installed.length > 0 && (
        <Card title="Installed" description="Apps installed from the catalog on this server.">
          <ul className="divide-y divide-border text-sm">
            {installed.map((i) => (
              <li key={i.name} className="flex flex-wrap items-center justify-between gap-2 py-2">
                <div className="flex items-center gap-2.5">
                  <AppIcon slug={i.slug} name={i.name} size="sm" />
                  <div><span className="font-medium">{i.name}</span> <span className="ml-2 text-xs text-ink-muted">{i.slug} · {new Date(i.installedAt).toLocaleDateString()}</span></div>
                </div>
                <div className="flex items-center gap-3 text-xs">
                  {i.domain && <a href={`https://${i.domain}`} target="_blank" rel="noreferrer" className="text-accent hover:underline">{i.domain}</a>}
                  {credentials(i.values).length > 0 && <details className="relative"><summary className="cursor-pointer text-ink-muted hover:text-ink">Credentials</summary><pre className="absolute right-0 z-10 mt-1 max-w-md rounded-md border border-border bg-surface p-2 font-mono text-[11px] shadow-float">{credentials(i.values).map(([k, v]) => `${k}=${v}`).join("\n")}</pre></details>}
                  {updates[i.name] && (canInstall ? <button type="button" disabled={updating !== null} onClick={async () => { setUpdating(i.name); try { await postStream(`/api/v1/catalog/installed/${i.name}/update`, () => {}); setUpdates((u) => { const c = { ...u }; delete c[i.name]; return c; }); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setUpdating(null); } }} className="text-warning hover:underline">{updating === i.name ? "Updating…" : "Update available"}</button> : <span className="text-warning">Update available</span>)}
                  <Link to={`/containers/stacks?stack=${encodeURIComponent(i.name)}`} className="text-ink-muted hover:text-ink">Manage stack</Link>
                </div>
              </li>
            ))}
          </ul>
        </Card>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search apps" className="h-8 max-w-xs text-xs" />
        <div className="flex flex-wrap gap-1">
          {cats.map((c) => <button key={c} type="button" onClick={() => pickCategory(c)} className={`rounded-sm border px-2 py-0.5 text-xs ${cat === c ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted hover:text-ink"}`}>{c}</button>)}
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {shown.map((a) => (
          <button key={a.slug} type="button" onClick={() => setSel(a)} className="flex gap-3 rounded-lg border border-border bg-surface p-4 text-left transition-colors hover:border-border-strong">
            <AppIcon slug={a.slug} category={a.category} name={a.name} />
            <span className="min-w-0 flex-1">
              <span className="flex items-center justify-between gap-2"><span className="truncate font-semibold">{a.name}</span><span className="shrink-0 font-mono text-[11px] text-ink-faint">{a.category}</span></span>
              <span className="mt-1 block text-sm text-ink-muted">{a.description}</span>
              {a.needsDomain && <span className="mt-2 block text-[11px] text-ink-faint">Needs a domain</span>}
            </span>
          </button>
        ))}
      </div>

      {sel && <Installer app={sel} canInstall={canInstall} onClose={() => setSel(null)} onDone={load} />}
    </>}
    </div>
  );
}

function Installer({ app, canInstall, onClose, onDone }: { app: CatalogApp; canInstall: boolean; onClose: () => void; onDone: () => Promise<void> }) {
  const [detail, setDetail] = useState<CatalogApp | null>(null);
  const [name, setName] = useState(app.slug);
  const [domain, setDomain] = useState("");
  const [tls, setTls] = useState<"letsencrypt" | "self" | "none">("letsencrypt");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [out, setOut] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [showCompose, setShowCompose] = useState(false);
  useEffect(() => { api.catalogApp(app.slug).then(setDetail).catch(() => setDetail(app)); }, [app]);

  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setOut([]); setMsg(null);
    try {
      await postStream(`/api/v1/catalog/${app.slug}/install`, (l) => setOut((o) => [...(o ?? []).slice(-300), l]), { name, fields, domain: domain.trim(), tls });
      setMsg(domain ? `Installed. Open https://${domain} once DNS points here.` : "Installed. Find it under Containers.");
      await onDone();
    } catch (er) { setMsg(String(er instanceof Error ? er.message : er)); }
    finally { setBusy(false); }
  };
  const suggest = async () => { try { const r = await api.previewHost(name); if (r.host) { setDomain(r.host); setTls("self"); } } catch { /* ignore */ } };

  return (
    <Card title={`Install ${app.name}`} description={app.description} icon={<AppIcon slug={app.slug} category={app.category} name={app.name} />}>
      <form onSubmit={submit} className="grid gap-4 md:grid-cols-2">
        <Field label="Stack name" hint="Lowercase, digits and dashes. Containers are named after it."><Input value={name} onChange={(e) => setName(e.target.value)} required /></Field>
        {app.category === "database" ? (
          <Field label="Domain" hint="Databases speak their own protocol, so a domain would not reach this one. Other containers connect by name, and the Databases page opens a browser client for you.">
            <p className="text-sm text-ink-muted">Not used for databases.</p>
          </Field>
        ) : (
        <Field label={app.needsDomain ? "Domain (required)" : "Domain (optional)"} hint="Routes the app through the proxy with HTTPS.">
          <div className="flex gap-2"><Input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="app.example.com" required={app.needsDomain} /><Button type="button" variant="secondary" onClick={() => void suggest()}>Preview</Button></div>
        </Field>
        )}
        {domain && app.category !== "database" && <Field label="Certificate"><Select value={tls} onChange={(e) => setTls(e.target.value as typeof tls)}><option value="letsencrypt">Let's Encrypt</option><option value="self">Self-signed</option><option value="none">HTTP only</option></Select></Field>}
        {(detail?.fields ?? []).map((f) => (
          <Field key={f.key} label={f.label} hint={f.type === "secret" ? "Leave empty to generate a strong value." : f.hint}>
            <Input value={fields[f.key] ?? (f.type === "secret" ? "" : f.default)} onChange={(e) => setFields({ ...fields, [f.key]: e.target.value })} type={f.type === "password" ? "password" : "text"} placeholder={f.type === "secret" ? "generated" : ""} />
          </Field>
        ))}
        {detail?.notes && <p className="text-sm text-ink-muted md:col-span-2">{detail.notes}</p>}
        <div className="flex flex-wrap items-center gap-2 md:col-span-2">
          {canInstall && <Button type="submit" disabled={busy}>{busy ? "Installing…" : "Install"}</Button>}
          <Button type="button" variant="secondary" onClick={() => setShowCompose((v) => !v)}>{showCompose ? "Hide" : "Show"} compose file</Button>
          <Button type="button" variant="secondary" onClick={onClose}>Close</Button>
          {app.website && <a href={app.website} target="_blank" rel="noreferrer" className="text-sm text-accent hover:underline">Project website</a>}
          {msg && <span className="text-sm text-ink-muted">{msg}</span>}
        </div>
      </form>
      {showCompose && detail?.compose && <pre className="mt-3 max-h-72 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{detail.compose}</pre>}
      {out && <pre className="mt-3 max-h-56 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{out.join("\n") || "…"}</pre>}
    </Card>
  );
}

/** The values worth showing. ISLET_DOMAIN is plumbing, not a credential, so an
    app whose only value is that one has nothing to reveal. */
function credentials(values?: Record<string, string>): [string, string][] {
  return Object.entries(values ?? {}).filter(([k]) => k !== "ISLET_DOMAIN");
}
