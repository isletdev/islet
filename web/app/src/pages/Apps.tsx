import { lazy, Suspense, useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";

const Deploys = lazy(() => import("./Deploys"));
const RecipesPage = lazy(() => import("./Recipes"));
import { api, RequestError, type CatalogApp, type InstalledApp } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useDialog, failure } from "@/lib/dialogs";
import { postStream } from "@/lib/stream";
import { Alert, Button, Card, Field, Input, Select, Tab, Tabs } from "@/components/ui";
import AppIcon from "@/components/AppIcon";
import { ExternalIcon, TrashIcon } from "@/components/icons";

export default function Apps() {
  const { state } = useAuth();
  const ask = useDialog();
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

  const installedSlugs = useMemo(() => new Set(installed.map((i) => i.slug)), [installed]);

  const update = async (name: string) => {
    setUpdating(name);
    try {
      await postStream(`/api/v1/catalog/installed/${name}/update`, () => {});
      setUpdates((u) => { const c = { ...u }; delete c[name]; return c; });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setUpdating(null);
    }
  };

  // Removing an app is removing its containers; its data is a separate
  // question, asked separately, because only one of the two can be undone.
  const remove = async (app: InstalledApp) => {
    const keep = await ask.confirm({
      title: `Remove ${app.name}?`,
      body: (
        <div className="space-y-2">
          <p>The containers are stopped and deleted, and so is the Compose file Islet wrote for it.</p>
          <p className="text-ink-muted">
            Its data volumes are kept unless you say otherwise on the next question. {app.domain
              ? `The domain ${app.domain} stays routed until you remove it on the Domains page.`
              : ""}
          </p>
        </div>
      ),
      confirmLabel: "Remove it",
      tone: "danger",
      typeToConfirm: app.name,
    });
    if (!keep) return;
    const volumes = await ask.confirm({
      title: `Delete ${app.name}'s data as well?`,
      body: "Its volumes hold the database and the uploads. Deleting them cannot be undone, and keeping them means a reinstall with the same name finds its data again.",
      confirmLabel: "Delete the data too",
      cancelLabel: "Keep the data",
      tone: "danger",
    });
    try {
      await api.stackRemove(app.name, volumes);
      await load();
    } catch (e) {
      void ask.alert({ title: `Could not remove ${app.name}`, body: failure(e), tone: "danger" });
    }
  };

  const cats = useMemo(() => ["all", ...Array.from(new Set(apps.map((a) => a.category))).sort()], [apps]);
  const shown = apps.filter((a) => (cat === "all" || a.category === cat) && (!q || `${a.name} ${a.description}`.toLowerCase().includes(q.toLowerCase())));

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Apps</h1>
        <p className="mt-1 text-ink-muted">Deploy your own code from Git, or install one-click apps from the catalog.</p>
      </div>
      <Tabs label="App sections">
        {(["deploys", "catalog", "recipes"] as const).map((t) => (
          <Tab key={t} active={tab === t} onClick={() => setParams(t === "deploys" ? {} : { tab: t })}>
            {t === "deploys" ? "Your apps" : t === "catalog" ? "Catalog" : "Recipes"}
          </Tab>
        ))}
      </Tabs>
      {tab === "deploys" && (
        <div className="space-y-6">
          {err && <Alert>{err}</Alert>}
          <Installed
            apps={installed}
            updates={updates}
            updating={updating}
            canInstall={canInstall}
            onUpdate={update}
            onRemove={remove}
            onBrowse={() => setParams({ tab: "catalog" })}
          />
          <Suspense fallback={<p className="text-sm text-ink-muted">Loading…</p>}><Deploys /></Suspense>
        </div>
      )}
      {tab === "recipes" && <Suspense fallback={<p className="text-sm text-ink-muted">Loading…</p>}><RecipesPage /></Suspense>}
      {tab === "catalog" && <>
      {err && <Alert>{err}</Alert>}

      <div className="flex flex-wrap items-center gap-2">
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search apps" className="h-8 max-w-xs text-xs" />
        <div className="flex flex-wrap gap-1">
          {cats.map((c) => <button key={c} type="button" onClick={() => pickCategory(c)} className={`rounded-sm border px-2 py-0.5 text-xs ${cat === c ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted hover:text-ink"}`}>{c}</button>)}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {shown.map((a) => (
          <button
            key={a.slug}
            type="button"
            onClick={() => setSel(a)}
            className="flex h-full gap-3 rounded-lg border border-border bg-surface p-4 text-left transition-colors hover:border-border-strong"
          >
            <AppIcon slug={a.slug} category={a.category} name={a.name} />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="flex items-center justify-between gap-2">
                <span className="truncate font-semibold">{a.name}</span>
                <span className="shrink-0 font-mono text-[11px] text-ink-faint">{a.category}</span>
              </span>
              {/* Three lines, always: a card that grows with its description
                  drags the whole row with it, and a grid of different-sized
                  cards reads as a mistake rather than as a catalogue. */}
              <span className="mt-1 line-clamp-3 block min-h-[3.75rem] text-sm text-ink-muted">{a.description}</span>
              {/* Reserved whether or not there is anything to say, so a card
                  with a badge is not taller than one without. */}
              <span className="mt-auto block min-h-6 pt-2 text-[11px] leading-4 text-ink-faint">
                {installedSlugs.has(a.slug)
                  ? <span className="rounded-sm bg-success-soft px-1 text-success">Installed</span>
                  : a.needsDomain ? "Needs a domain" : ""}
              </span>
            </span>
          </button>
        ))}
      </div>

      {sel && (
        <Installer
          app={sel}
          canInstall={canInstall}
          existing={installed.filter((i) => i.slug === sel.slug)}
          onClose={() => setSel(null)}
          onDone={load}
          onSeeApps={() => { setSel(null); setParams({}); }}
        />
      )}
    </>}
    </div>
  );
}

/** The first of `slug`, `slug-2`, `slug-3`… that nothing is called yet. */
function freeName(slug: string, taken: string[]): string {
  const used = new Set(taken);
  if (!used.has(slug)) return slug;
  for (let n = 2; n < 100; n++) {
    if (!used.has(`${slug}-${n}`)) return `${slug}-${n}`;
  }
  return slug;
}

/**
 * Installing one app.
 *
 * A dialog rather than a panel below the grid: it used to appear under the
 * cards, off the bottom of the screen, so clicking a card looked like it had
 * done nothing at all.
 *
 * It also stays open when the install finishes, saying what happened and where
 * the app now is. A form that resets itself and offers Install again is an
 * invitation to install the same thing twice.
 */
function Installer({ app, canInstall, existing, onClose, onDone, onSeeApps }: { app: CatalogApp; canInstall: boolean; existing: InstalledApp[]; onClose: () => void; onDone: () => Promise<void>; onSeeApps: () => void }) {
  const [detail, setDetail] = useState<CatalogApp | null>(null);
  // A second copy gets a name of its own. Offering a name that is already
  // taken is offering a failure, and the person has no way to know it yet.
  const [name, setName] = useState(() => freeName(app.slug, existing.map((i) => i.name)));
  const [domain, setDomain] = useState("");
  const [tls, setTls] = useState<"letsencrypt" | "self" | "none">("letsencrypt");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [out, setOut] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [showCompose, setShowCompose] = useState(false);
  const [done, setDone] = useState<{ name: string; domain: string } | null>(null);
  useEffect(() => { api.catalogApp(app.slug).then(setDetail).catch(() => setDetail(app)); }, [app]);
  // Escape closes it, the way every other dialog in the panel does.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape" && !busy) onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setOut([]); setMsg(null);
    try {
      await postStream(`/api/v1/catalog/${app.slug}/install`, (l) => setOut((o) => [...(o ?? []).slice(-300), l]), { name, fields, domain: domain.trim(), tls });
      setDone({ name, domain: domain.trim() });
      await onDone();
    } catch (er) { setMsg(String(er instanceof Error ? er.message : er)); }
    finally { setBusy(false); }
  };
  const suggest = async () => { try { const r = await api.previewHost(name); if (r.host) { setDomain(r.host); setTls("self"); } } catch { /* ignore */ } };

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 py-10"
      onClick={() => { if (!busy) onClose(); }}
    >
      <div className="w-full max-w-3xl" onClick={(e) => e.stopPropagation()}>
        <Card
          title={done ? `${app.name} is installed` : `Install ${app.name}`}
          description={done ? "It is running. Nothing else here needs doing." : app.description}
          icon={<AppIcon slug={app.slug} category={app.category} name={app.name} />}
        >
        {done ? (
          <div className="space-y-3">
            <dl className="flex flex-wrap gap-x-8 gap-y-2 text-sm">
              <div>
                <dt className="text-[11px] text-ink-faint">Stack</dt>
                <dd className="font-mono">{done.name}</dd>
              </div>
              {done.domain && (
                <div>
                  <dt className="text-[11px] text-ink-faint">Address</dt>
                  <dd>
                    <a href={`https://${done.domain}`} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                      https://{done.domain}
                    </a>
                  </dd>
                </div>
              )}
            </dl>
            <p className="text-sm text-ink-muted">
              {done.domain
                ? "The certificate arrives once DNS points at this server; until then the address may not answer."
                : "It has no domain, so reach it from another container by name, or give it one on the Domains page."}
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" onClick={onSeeApps}>See it in Your apps</Button>
              {done.domain && (
                <a
                  href={`https://${done.domain}`}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3 text-sm hover:bg-surface-2"
                >
                  <ExternalIcon className="h-4 w-4" />Open it
                </a>
              )}
              <Button type="button" variant="secondary" onClick={onClose}>Install something else</Button>
            </div>
            {out && <pre className="max-h-56 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{out.join("\n")}</pre>}
          </div>
        ) : (
        <form onSubmit={submit} className="grid grid-cols-1 gap-4 md:grid-cols-2">
        {/* Installing the same app twice is a real mistake to make: the catalog
            card looks the same whether you have it or not, and the second copy
            is a whole separate stack with its own data. Say so before the
            Install button, not after. */}
        {existing.length > 0 && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm md:col-span-2">
            <span>
              {app.name} is already installed as{" "}
              {existing.map((i, n) => (
                <span key={i.name}>{n > 0 && ", "}<span className="font-mono">{i.name}</span></span>
              ))}.
            </span>
            <span className="text-ink-muted">Installing it again makes a second copy, separate from the first.</span>
            <button type="button" onClick={onSeeApps} className="-my-1 py-1 text-accent hover:underline">Go to it</button>
          </div>
        )}
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
      )}
      {!done && showCompose && detail?.compose && <pre className="mt-3 max-h-72 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{detail.compose}</pre>}
      {!done && out && <pre className="mt-3 max-h-56 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{out.join("\n") || "…"}</pre>}
        </Card>
      </div>
    </div>
  );
}

/** Everything installed from the catalog, where somebody would look for it. */
function Installed({
  apps, updates, updating, canInstall, onUpdate, onRemove, onBrowse,
}: {
  apps: InstalledApp[];
  updates: Record<string, string[]>;
  updating: string | null;
  canInstall: boolean;
  onUpdate: (name: string) => Promise<void>;
  onRemove: (app: InstalledApp) => Promise<void>;
  onBrowse: () => void;
}) {
  if (apps.length === 0) {
    return (
      <Card title="From the catalog" description="One-click apps installed on this server.">
        <div className="flex flex-wrap items-center gap-3">
          <p className="text-sm text-ink-muted">Nothing installed yet.</p>
          <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={onBrowse}>Browse the catalog</Button>
        </div>
      </Card>
    );
  }
  return (
    <Card title="From the catalog" description="One-click apps installed on this server.">
      <ul className="divide-y divide-border text-sm">
        {apps.map((i) => (
          <li key={i.name} className="flex flex-wrap items-center justify-between gap-2 py-2">
            <div className="flex items-center gap-2.5">
              <AppIcon slug={i.slug} name={i.name} size="sm" />
              <div>
                <span className="font-medium">{i.name}</span>
                <span className="ml-2 text-xs text-ink-muted">{i.slug} · {new Date(i.installedAt).toLocaleDateString()}</span>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-3 text-xs">
              {i.domain && <a href={`https://${i.domain}`} target="_blank" rel="noreferrer" className="-my-1 inline-block py-1 text-accent hover:underline">{i.domain}</a>}
              {credentials(i.values).length > 0 && (
                <details className="relative">
                  <summary className="cursor-pointer text-ink-muted hover:text-ink">Credentials</summary>
                  <pre className="absolute right-0 z-10 mt-1 max-w-md rounded-md border border-border bg-surface p-2 font-mono text-[11px] shadow-float">{credentials(i.values).map(([k, v]) => `${k}=${v}`).join("\n")}</pre>
                </details>
              )}
              {updates[i.name] && (canInstall
                ? <button type="button" disabled={updating !== null} onClick={() => void onUpdate(i.name)} className="-my-1 py-1 text-warning hover:underline">{updating === i.name ? "Updating…" : "Update available"}</button>
                : <span className="text-warning">Update available</span>)}
              <Link to={`/containers/stacks?stack=${encodeURIComponent(i.name)}`} className="-my-1 py-1 text-ink-muted hover:text-ink">Manage stack</Link>
              {canInstall && (
                <button type="button" onClick={() => void onRemove(i)} className="-my-1 inline-flex items-center gap-1 py-1 text-danger hover:underline">
                  <TrashIcon className="h-3.5 w-3.5" />Remove
                </button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}

/** The values worth showing. ISLET_DOMAIN is plumbing, not a credential, so an
    app whose only value is that one has nothing to reveal. */
function credentials(values?: Record<string, string>): [string, string][] {
  return Object.entries(values ?? {}).filter(([k]) => k !== "ISLET_DOMAIN");
}
