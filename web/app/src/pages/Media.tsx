import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import {
  api, RequestError,
  type MediaBucket, type MediaKey, type MediaObject, type MediaOverview, type MediaPreset,
} from "@/lib/api";
import { postStream } from "@/lib/stream";
import { useDialog } from "@/lib/dialogs";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { CopyIcon, TrashIcon } from "@/components/icons";

function fail(e: unknown) {
  if (e instanceof RequestError) return e.message;
  return e instanceof Error ? e.message : String(e);
}

/** What the converters can take a picture of. Mirrors the daemon's own list. */
function thumbnailable(contentType: string) {
  return contentType.startsWith("image/") || contentType.startsWith("video/") || contentType === "application/pdf";
}

function size(n: number) {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

/**
 * The media service, from the operator's side.
 *
 * Everything else in this panel is the operator acting on their server. This
 * page is the operator setting up something their *applications* will use, and
 * the difference shows: what is configured here is a base URL, a place to put
 * bytes, and credentials that leave this building. So the page ends with the
 * code somebody pastes into their app, filled in with this server's own address
 * — "plug and play" means not reading a specification first.
 */
export default function Media() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const ask = useDialog();
  const [over, setOver] = useState<MediaOverview | null>(null);
  const [buckets, setBuckets] = useState<MediaBucket[]>([]);
  const [keys, setKeys] = useState<MediaKey[]>([]);
  const [presets, setPresets] = useState<MediaPreset[]>([]);
  const [objects, setObjects] = useState<MediaObject[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [installing, setInstalling] = useState<string[] | null>(null);
  // The one time a key's secret exists anywhere but in the application holding
  // it. Kept on screen until it is dismissed, because a page that scrolls it
  // away is a key somebody has to delete and mint again.
  const [minted, setMinted] = useState<MediaKey | null>(null);

  const load = useCallback(async () => {
    try {
      const [o, b, k, p, obj] = await Promise.all([
        api.media(), api.mediaBuckets(), api.mediaKeys(), api.mediaPresets(), api.mediaObjects(),
      ]);
      setOver(o); setBuckets(b); setKeys(k); setPresets(p); setObjects(obj);
    } catch (e) { setErr(fail(e)); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const run = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key); setErr(null);
    try { await fn(); await load(); }
    catch (e) { setErr(fail(e)); }
    finally { setBusy(null); }
  };

  if (!isAdmin) {
    return <div className="mx-auto max-w-2xl"><Alert>The media service is configured by admins.</Alert></div>;
  }
  if (!over) return null;

  const s = over.settings;
  const base = s.host ? `https://${s.host}/v1` : `${location.origin}/svc/media/v1`;

  const installTools = async () => {
    setInstalling([]); setErr(null);
    try {
      await postStream("/api/v1/media/worker", (l) => setInstalling((o) => [...(o ?? []).slice(-200), l]));
      await load();
    } catch (e) { setErr(fail(e)); }
    finally { setInstalling(null); }
  };

  return (
    <div className="mx-auto max-w-5xl space-y-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Media</h1>
        <p className="mt-1 text-ink-muted">
          An upload and image API your applications call. They send a file and get a URL; where the bytes
          live and what sizes exist is your decision, not theirs.
        </p>
      </div>
      {err && <Alert>{err}</Alert>}

      <Card title="The service" description="Off by default, and off costs nothing: no container, no port, no traffic.">
        <SettingsForm settings={s} busy={busy === "settings"} onSave={(next) => void run("settings", () => api.mediaSave(next))} />
        {s.enabled && (
          <p className="mt-3 text-xs text-ink-muted">
            Answering at <code className="font-mono text-ink">{base}</code>
            {!s.host && " — set a hostname above and point a domain at this panel to give browsers an origin of their own."}
          </p>
        )}
        {/* Saving a hostname adds the domain for it. What Islet cannot do from
            here is the DNS record, so that is the one thing left to say. */}
        {s.enabled && s.host && over.hostRouted && (
          <p className="mt-1.5 text-xs text-ink-muted">
            <Link to="/domains" className="-my-1 inline-block py-1 underline">A domain for it</Link> is set up and pointed at this panel.
            Give <code className="font-mono">{s.host}</code> an A record for this server and the certificate follows on its own.
          </p>
        )}
        {s.enabled && s.host && !over.hostRouted && (
          <Alert tone="warning">
            Nothing routes <code className="font-mono">{s.host}</code> here, so requests to it get the proxy's 404.
            Saving the hostname again adds the domain; if that keeps failing, the name is probably taken by another
            domain under <Link to="/domains" className="-my-1 inline-block py-1 underline">Domains</Link>.
          </Alert>
        )}
      </Card>

      <Card
        title="Converters"
        description="libvips, ffmpeg and poppler, in a container Islet builds and owns. Around 300 MB, pulled only when you ask for it."
      >
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <span className={`inline-flex items-center gap-1.5 ${over.tools.running ? "text-success" : "text-ink-muted"}`}>
            <span className={`inline-block h-2 w-2 rounded-full ${over.tools.running ? "bg-success" : "bg-border-strong"}`} />
            {over.tools.running ? "running" : "not installed"}
          </span>
          {over.tools.running && (
            <span className="text-xs text-ink-muted">
              vips {over.tools.vips} · ffmpeg {over.tools.ffmpeg} · {over.tools.poppler}
            </span>
          )}
          <span className="ml-auto flex gap-2">
            <Button className="h-8 text-xs" disabled={installing !== null} onClick={() => void installTools()}>
              {installing !== null ? "Building…" : over.tools.running ? "Rebuild" : "Install"}
            </Button>
            {over.tools.running && (
              <Button variant="secondary" className="h-8 text-xs" disabled={busy !== null}
                onClick={() => void run("worker", () => api.mediaWorkerRemove())}>Remove</Button>
            )}
          </span>
        </div>
        {installing && installing.length > 0 && (
          <pre className="mt-2 max-h-40 overflow-auto rounded-md bg-code-bg p-2 text-xs">{installing.join("\n")}</pre>
        )}
        {!over.tools.running && (
          <p className="mt-2 text-xs text-ink-muted">
            Files can be uploaded and served without these. Resizing, thumbnails of a PDF's first page and a
            frame out of a video need them.
          </p>
        )}
      </Card>

      <Buckets list={buckets} busy={busy} onRun={run} ask={ask} />
      <Keys list={keys} buckets={buckets} busy={busy} minted={minted} onMinted={setMinted} onRun={run} ask={ask} />
      <Presets list={presets} busy={busy} onRun={run} />
      <Objects list={objects} base={base} busy={busy} onRun={run} ask={ask} />

      {over.usage.length > 0 && (
        <Card title="What is stored" description="By namespace, which is how one application's files are kept apart from another's.">
          <ul className="divide-y divide-border text-sm">
            {over.usage.map((u) => (
              <li key={u.namespace} className="flex items-center justify-between py-1.5">
                <span>{u.namespace || <span className="text-ink-muted">no namespace</span>}</span>
                <span className="text-ink-muted">{u.objects} objects · {size(u.bytes)}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}

      <Card title="Using it from an application" description="Against this server, with a key you minted above.">
        <Snippet base={base} presets={presets} />
      </Card>
    </div>
  );
}

function SettingsForm({ settings, busy, onSave }: { settings: { enabled: boolean; host: string; transforms: number; maxBytes: number }; busy: boolean; onSave: (s: { enabled: boolean; host: string; transforms: number; maxBytes: number }) => void }) {
  const [form, setForm] = useState(settings);
  useEffect(() => { setForm(settings); }, [settings]);
  return (
    <form className="space-y-3" onSubmit={(e: FormEvent) => { e.preventDefault(); onSave(form); }}>
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} className="mt-0.5" />
        <span>
          <span className="font-medium">Enabled</span>
          <span className="block text-xs text-ink-muted">Applications with a key can upload and fetch. Switching it off stops answering immediately.</span>
        </span>
      </label>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <Field label="Hostname" hint="A domain pointed at this panel, for browsers. Optional.">
          <Input value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder="media.example.com" className="font-mono" />
        </Field>
        <Field label="Conversions at once" hint="One is right on a small server.">
          <Input type="number" min={1} max={8} value={form.transforms} onChange={(e) => setForm({ ...form, transforms: Number(e.target.value) })} />
        </Field>
        <Field label="Largest upload" hint="Megabytes, unless a bucket says otherwise.">
          <Input type="number" min={1} value={Math.round(form.maxBytes / (1 << 20))} onChange={(e) => setForm({ ...form, maxBytes: Number(e.target.value) * (1 << 20) })} />
        </Field>
      </div>
      <Button type="submit" className="h-9 text-xs" disabled={busy}>Save</Button>
    </form>
  );
}

type Runner = (key: string, fn: () => Promise<unknown>) => Promise<void>;
type Ask = ReturnType<typeof useDialog>;

function Buckets({ list, busy, onRun, ask }: { list: MediaBucket[]; busy: string | null; onRun: Runner; ask: Ask }) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<Partial<MediaBucket> & { secret?: string }>({ driver: "local", scanUploads: true, config: {} });
  const cfg = (k: string) => form.config?.[k] ?? "";
  const setCfg = (k: string, v: string) => setForm({ ...form, config: { ...(form.config ?? {}), [k]: v } });
  return (
    <Card title="Buckets" description="Where the bytes go. One driver covers S3, Cloudflare R2, MinIO, Backblaze and the rest — they all speak the same API.">
      <ul className="divide-y divide-border text-sm">
        {list.map((b) => (
          <li key={b.id} className="grid grid-cols-1 items-center gap-x-3 gap-y-1 py-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
            <div className="min-w-0">
              <span className="font-medium">{b.name}</span>
              {b.default && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px]">default</span>}
              <div className="truncate text-xs text-ink-muted">
                {b.driver === "local" ? "on this server" : `${b.config?.endpoint ?? ""}/${b.config?.bucket ?? ""}`}
                {b.scanUploads ? " · scanned" : " · not scanned"}
                {b.publicBase ? ` · public at ${b.publicBase}` : ""}
              </div>
            </div>
            <button type="button" className="-my-1 py-1 text-xs text-ink-muted hover:text-ink" disabled={busy !== null}
              onClick={() => void onRun("check" + b.id, async () => { await api.mediaBucketCheck(b.id); await ask.alert({ title: `${b.name} works`, body: "A small object was written, read back and removed." }); })}>
              Check
            </button>
            <button type="button" aria-label={`Delete ${b.name}`} className="-my-1 justify-self-end rounded-md p-1 text-ink-muted hover:text-danger"
              onClick={() => void onRun("rm" + b.id, async () => {
                if (!(await ask.confirm({ title: `Delete the bucket ${b.name}?`, body: "Only possible while nothing is stored in it.", confirmLabel: "Delete", tone: "danger" }))) return;
                await api.mediaBucketRemove(b.id);
              })}>
              <TrashIcon className="h-3.5 w-3.5" />
            </button>
          </li>
        ))}
        {list.length === 0 && <li className="py-2 text-xs text-ink-muted">No bucket yet. Add one on this server to start without an account anywhere.</li>}
      </ul>
      {!open ? (
        <Button variant="secondary" className="mt-3 h-8 text-xs" onClick={() => setOpen(true)}>Add a bucket</Button>
      ) : (
        <form className="mt-3 space-y-3 border-t border-border pt-3"
          onSubmit={(e: FormEvent) => { e.preventDefault(); void onRun("bucket", async () => { await api.mediaBucketSave(form); setOpen(false); setForm({ driver: "local", scanUploads: true, config: {} }); }); }}>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field label="Name"><Input value={form.name ?? ""} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="uploads" required /></Field>
            <Field label="Driver">
              <Select value={form.driver} onChange={(e) => setForm({ ...form, driver: e.target.value as "local" | "s3" })}>
                <option value="local">This server's disk</option>
                <option value="s3">S3-compatible</option>
              </Select>
            </Field>
            {/* Only for a bucket that is already reachable somewhere else. On
                a bucket held here there is no second address to send anyone
                to, so the field is not offered rather than offered and
                refused. */}
            {form.driver === "s3" ? (
              <Field label="Public base URL" hint="Where this bucket is already public — an R2 custom domain, a CDN in front of S3. Optional.">
                <Input value={form.publicBase ?? ""} onChange={(e) => setForm({ ...form, publicBase: e.target.value })} placeholder="https://cdn.example.com" className="font-mono" />
              </Field>
            ) : (
              <Field label="Served by" hint="Islet serves these objects itself. To put a CDN in front, point it at the media hostname above.">
                <Input value="this server" disabled readOnly />
              </Field>
            )}
          </div>
          {form.driver === "s3" && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <Field label="Endpoint"><Input value={cfg("endpoint")} onChange={(e) => setCfg("endpoint", e.target.value)} placeholder="https://<id>.r2.cloudflarestorage.com" className="font-mono" required /></Field>
              <Field label="Bucket"><Input value={cfg("bucket")} onChange={(e) => setCfg("bucket", e.target.value)} required /></Field>
              <Field label="Region" hint="auto for R2."><Input value={cfg("region")} onChange={(e) => setCfg("region", e.target.value)} placeholder="auto" /></Field>
              <Field label="Access key"><Input value={form.accessKey ?? ""} onChange={(e) => setForm({ ...form, accessKey: e.target.value })} className="font-mono" /></Field>
              <Field label="Secret key" hint="Stored sealed, never shown again."><Input type="password" value={form.secret ?? ""} onChange={(e) => setForm({ ...form, secret: e.target.value })} className="font-mono" /></Field>
              <Field label="Prefix" hint="A folder inside the bucket. Optional."><Input value={cfg("prefix")} onChange={(e) => setCfg("prefix", e.target.value)} className="font-mono" /></Field>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={cfg("pathStyle") === "1"} onChange={(e) => setCfg("pathStyle", e.target.checked ? "1" : "")} />
                Path-style addressing (MinIO and most self-hosted servers)
              </label>
            </div>
          )}
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field label="Accepted types" hint="image/* — comma separated. Empty accepts anything.">
              <Input value={form.allowTypes ?? ""} onChange={(e) => setForm({ ...form, allowTypes: e.target.value })} placeholder="image/*, application/pdf" className="font-mono" />
            </Field>
            <div className="space-y-2 pt-5">
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={form.scanUploads ?? true} onChange={(e) => setForm({ ...form, scanUploads: e.target.checked })} />
                Scan uploads for malware
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={form.default ?? false} onChange={(e) => setForm({ ...form, default: e.target.checked })} />
                Use this by default
              </label>
            </div>
          </div>
          <div className="flex gap-2">
            <Button type="submit" className="h-9 text-xs" disabled={busy !== null}>Save bucket</Button>
            <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => setOpen(false)}>Cancel</Button>
          </div>
        </form>
      )}
    </Card>
  );
}

function Keys({ list, buckets, busy, minted, onMinted, onRun, ask }: {
  list: MediaKey[]; buckets: MediaBucket[]; busy: string | null;
  minted: MediaKey | null; onMinted: (k: MediaKey | null) => void; onRun: Runner; ask: Ask;
}) {
  const [form, setForm] = useState<Partial<MediaKey>>({ scopes: "upload,read" });
  const toggle = (scope: string) => {
    const have = (form.scopes ?? "").split(",").map((x) => x.trim()).filter(Boolean);
    const next = have.includes(scope) ? have.filter((x) => x !== scope) : [...have, scope];
    setForm({ ...form, scopes: next.join(",") });
  };
  return (
    <Card title="Keys" description="What an application holds. It carries no authority on this server beyond its own namespace, and the panel API refuses it.">
      {minted?.secret && (
        <Alert tone="warning">
          <div className="space-y-2">
            <p><span className="font-medium">{minted.name}</span> is the only time this key is shown. Put it in the application now.</p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-md bg-code-bg px-2 py-1 font-mono text-xs">{minted.secret}</code>
              <button type="button" aria-label="Copy the key" className="rounded-md p-1 text-ink-muted hover:text-ink"
                onClick={() => void navigator.clipboard.writeText(minted.secret ?? "")}>
                <CopyIcon className="h-4 w-4" />
              </button>
              <Button variant="secondary" className="h-8 text-xs" onClick={() => onMinted(null)}>Done</Button>
            </div>
          </div>
        </Alert>
      )}
      <ul className="mt-2 divide-y divide-border text-sm">
        {list.map((k) => (
          <li key={k.id} className="grid grid-cols-1 items-center gap-x-3 gap-y-1 py-2 sm:grid-cols-[minmax(0,1fr)_auto]">
            <div className="min-w-0">
              <span className="font-medium">{k.name}</span>
              <span className="ml-2 font-mono text-xs text-ink-muted">{k.prefix}…</span>
              <div className="truncate text-xs text-ink-muted">
                {k.namespace ? `namespace ${k.namespace}` : "every namespace"} · {k.scopes}
                {k.origins ? ` · from ${k.origins}` : " · server-side only"}
                {k.lastUsedAt ? ` · used ${new Date(k.lastUsedAt).toLocaleString()}` : " · never used"}
              </div>
            </div>
            <button type="button" aria-label={`Delete ${k.name}`} className="-my-1 justify-self-end rounded-md p-1 text-ink-muted hover:text-danger"
              onClick={() => void onRun("rmk" + k.id, async () => {
                if (!(await ask.confirm({ title: `Delete the key ${k.name}?`, body: "Anything using it stops working immediately.", confirmLabel: "Delete", tone: "danger" }))) return;
                await api.mediaKeyRemove(k.id);
              })}>
              <TrashIcon className="h-3.5 w-3.5" />
            </button>
          </li>
        ))}
        {list.length === 0 && <li className="py-2 text-xs text-ink-muted">No keys yet.</li>}
      </ul>
      <form className="mt-3 space-y-3 border-t border-border pt-3"
        onSubmit={(e: FormEvent) => { e.preventDefault(); void onRun("key", async () => { const k = await api.mediaKeyMint(form); onMinted(k); setForm({ scopes: "upload,read" }); }); }}>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
          <Field label="Name"><Input value={form.name ?? ""} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="shop" required /></Field>
          <Field label="Namespace" hint="Keeps one app's files apart."><Input value={form.namespace ?? ""} onChange={(e) => setForm({ ...form, namespace: e.target.value })} placeholder="shop" /></Field>
          <Field label="Bucket">
            <Select value={form.bucketId ?? ""} onChange={(e) => setForm({ ...form, bucketId: e.target.value })}>
              <option value="">the default</option>
              {buckets.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}
            </Select>
          </Field>
          <Field label="Browser origins" hint="Empty means no browser may use it.">
            <Input value={form.origins ?? ""} onChange={(e) => setForm({ ...form, origins: e.target.value })} placeholder="https://shop.example" className="font-mono" />
          </Field>
        </div>
        <div className="flex flex-wrap gap-3 text-sm">
          {["upload", "read", "delete", "sign"].map((sc) => (
            <label key={sc} className="flex items-center gap-1.5">
              <input type="checkbox" checked={(form.scopes ?? "").includes(sc)} onChange={() => toggle(sc)} />{sc}
            </label>
          ))}
        </div>
        <Button type="submit" className="h-9 text-xs" disabled={busy !== null}>Create key</Button>
      </form>
    </Card>
  );
}

function Presets({ list, busy, onRun }: { list: MediaPreset[]; busy: string | null; onRun: Runner }) {
  const [form, setForm] = useState<Partial<MediaPreset>>({ fit: "cover", format: "auto", quality: 80 });
  return (
    <Card
      title="Sizes"
      description="The sizes an application may ask for, by name. Named rather than free-form, because a server that resizes to anything on request is one anybody can stop with a loop."
    >
      <ul className="divide-y divide-border text-sm">
        {list.map((p) => (
          <li key={p.id} className="flex items-center justify-between py-1.5">
            <span><code className="font-mono">{p.name}</code>
              <span className="ml-2 text-xs text-ink-muted">{p.width || "auto"}×{p.height || "auto"} · {p.fit} · {p.format} · q{p.quality}</span>
            </span>
            <button type="button" aria-label={`Delete ${p.name}`} className="-my-1 rounded-md p-1 text-ink-muted hover:text-danger"
              onClick={() => void onRun("rmp" + p.id, () => api.mediaPresetRemove(p.id))}>
              <TrashIcon className="h-3.5 w-3.5" />
            </button>
          </li>
        ))}
      </ul>
      <form className="mt-3 grid grid-cols-2 gap-2 border-t border-border pt-3 sm:grid-cols-6"
        onSubmit={(e: FormEvent) => { e.preventDefault(); void onRun("preset", async () => { await api.mediaPresetSave(form); setForm({ fit: "cover", format: "auto", quality: 80 }); }); }}>
        <Input value={form.name ?? ""} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="name" required />
        <Input type="number" value={form.width ?? ""} onChange={(e) => setForm({ ...form, width: Number(e.target.value) })} placeholder="width" />
        <Input type="number" value={form.height ?? ""} onChange={(e) => setForm({ ...form, height: Number(e.target.value) })} placeholder="height" />
        <Select value={form.fit} onChange={(e) => setForm({ ...form, fit: e.target.value as "cover" | "contain" })}>
          <option value="cover">cover</option><option value="contain">contain</option>
        </Select>
        <Select value={form.format} onChange={(e) => setForm({ ...form, format: e.target.value })}>
          <option value="auto">webp</option><option value="avif">avif</option><option value="jpeg">jpeg</option><option value="png">png</option>
        </Select>
        <Button type="submit" className="h-9 text-xs" disabled={busy !== null}>Add</Button>
      </form>
    </Card>
  );
}

function Objects({ list, base, busy, onRun, ask }: { list: MediaObject[]; base: string; busy: string | null; onRun: Runner; ask: Ask }) {
  if (list.length === 0) {
    return <Card title="Recent uploads" description="Nothing yet. What your applications upload appears here."><span /></Card>;
  }
  return (
    <Card title="Recent uploads" description="What applications have put here, newest first.">
      <ul className="grid grid-cols-2 gap-3 sm:grid-cols-4 md:grid-cols-6">
        {list.map((o) => (
          <li key={o.id} className="group relative overflow-hidden rounded-md border border-border">
            {/* A picture where one can be made — an image, a video's frame, a
                PDF's first page. Not for a private object: this tag carries no
                credential, so it would draw a broken image and look like a
                fault rather than like the rule working. */}
            {thumbnailable(o.contentType) && o.visibility === "public" ? (
              <img src={`${base}/objects/${o.id}/thumb`} alt="" loading="lazy" className="h-24 w-full bg-surface-2 object-cover" />
            ) : (
              <div className="flex h-24 w-full items-center justify-center bg-surface-2 px-2 text-center text-[10px] text-ink-muted">
                {o.visibility === "private" ? "private" : o.contentType}
              </div>
            )}
            <div className="p-1.5">
              <div className="truncate text-[11px]" title={o.filename}>{o.filename}</div>
              <div className="truncate text-[10px] text-ink-muted">
                {size(o.size)}{o.visibility === "private" ? " · private" : ""}{o.scanner ? "" : " · unscanned"}
              </div>
            </div>
            <button type="button" aria-label={`Delete ${o.filename}`} disabled={busy !== null}
              className="absolute right-1 top-1 rounded-md bg-surface/90 p-1 text-ink-muted opacity-0 transition-opacity hover:text-danger focus:opacity-100 group-hover:opacity-100"
              onClick={() => void onRun("rmo" + o.id, async () => {
                if (!(await ask.confirm({ title: `Delete ${o.filename}?`, body: "The file and every size made from it go.", confirmLabel: "Delete", tone: "danger" }))) return;
                await api.mediaObjectRemove(o.id);
              })}>
              <TrashIcon className="h-3.5 w-3.5" />
            </button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

/** The bit that makes it plug and play: working code against this server. */
function Snippet({ base, presets }: { base: string; presets: MediaPreset[] }) {
  const [tab, setTab] = useState<"curl" | "js">("curl");
  const preset = presets[0]?.name ?? "thumb";
  const curl = `# upload
curl -X POST ${base}/upload \\
  -H "Authorization: Bearer $ISLET_MEDIA_KEY" \\
  -F "file=@photo.jpg"

# the answer carries the URL, and one per size you defined
# GET ${base}/objects/<id>
# GET ${base}/objects/<id>/${preset}`;
  const js = `const body = new FormData();
body.append("file", file);

const res = await fetch("${base}/upload", {
  method: "POST",
  headers: { Authorization: \`Bearer \${process.env.ISLET_MEDIA_KEY}\` },
  body,
});
const { id, url, variants } = await res.json();
// <img src={variants.${preset}} />`;
  const text = tab === "curl" ? curl : js;
  return (
    <div>
      <div className="mb-2 flex gap-2 text-xs">
        {(["curl", "js"] as const).map((k) => (
          <button key={k} type="button" onClick={() => setTab(k)}
            className={`-my-1 rounded-md px-2 py-1 ${tab === k ? "bg-surface-2 text-ink" : "text-ink-muted hover:text-ink"}`}>
            {k === "curl" ? "curl" : "JavaScript"}
          </button>
        ))}
        <button type="button" aria-label="Copy" className="-my-1 ml-auto rounded-md p-1 text-ink-muted hover:text-ink"
          onClick={() => void navigator.clipboard.writeText(text)}>
          <CopyIcon className="h-4 w-4" />
        </button>
      </div>
      <pre className="overflow-x-auto rounded-md bg-code-bg p-3 text-xs">{text}</pre>
    </div>
  );
}
