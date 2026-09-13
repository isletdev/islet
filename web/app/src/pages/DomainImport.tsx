import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, RequestError } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card } from "@/components/ui";
import { RefreshIcon } from "@/components/icons";

/**
 * Taking over from whatever is already serving this machine.
 *
 * Somebody installing Islet on a server that is already in use has their sites
 * written down somewhere, in whichever product they installed years ago. This
 * reads those files, shows what it found, and writes only the ones they tick.
 *
 * Three steps, and the middle one is the point: nothing is written until the
 * list has been read by a person. The scan only reads, so arriving here and
 * leaving again changes nothing at all.
 */

interface Found {
  source: string;
  files: string[];
  sites: {
    hosts: string[];
    upstream: string;
    root: string;
    rawUpstream?: string;
    tls: boolean;
    file: string;
    source?: string;
  }[];
}

interface Proposal {
  host: string;
  target: string;
  note: string;
  source?: string;
  file?: string;
  /** Extra paths on this host, each going somewhere of its own. */
  locations?: { path: string; target: string; stripPath: boolean; note?: string }[];
  /** Parts that were found and cannot be translated. Shown, never dropped. */
  skipped?: string[];
  saved: boolean;
}

type Step = "scan" | "review" | "done";

function err(e: unknown) {
  return e instanceof RequestError ? e.message : e instanceof Error ? e.message : String(e);
}

export default function DomainImport() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const navigate = useNavigate();

  const [step, setStep] = useState<Step>("scan");
  const [scanning, setScanning] = useState(true);
  const [found, setFound] = useState<Found[]>([]);
  const [text, setText] = useState("");
  const [proposals, setProposals] = useState<Proposal[]>([]);
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const scan = useCallback(async () => {
    setScanning(true);
    setError(null);
    try {
      setFound((await api.importScan()).found);
    } catch (e) {
      setError(err(e));
    } finally {
      setScanning(false);
    }
  }, []);

  useEffect(() => { if (isAdmin) void scan(); }, [isAdmin, scan]);

  const review = async (pasted: string) => {
    setBusy(true);
    setError(null);
    try {
      const list = await api.importSites({ text: pasted, save: false });
      setProposals(list);
      // Everything that can be imported is ticked: the common case is "all of
      // it", and un-ticking two is less work than ticking twenty.
      setChosen(new Set(list.filter((p) => p.target).map((p) => p.host)));
      setStep("review");
    } catch (e) {
      setError(err(e));
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    setBusy(true);
    setError(null);
    try {
      const list = await api.importSites({ text, save: true, hosts: [...chosen] });
      setProposals(list);
      setStep("done");
    } catch (e) {
      setError(err(e));
    } finally {
      setBusy(false);
    }
  };

  if (!isAdmin) {
    return <Alert>Only admins can import sites.</Alert>;
  }

  const total = found.reduce((n, f) => n + f.sites.length, 0);
  const importable = proposals.filter((p) => p.target);
  const savedCount = proposals.filter((p) => p.saved).length;

  return (
    <div className="mx-auto max-w-4xl space-y-4">
      <div>
        <Link to="/domains" className="-my-1 inline-block py-1 text-xs text-ink-muted hover:text-ink">← Domains</Link>
        <h1 className="mt-1 text-xl font-semibold tracking-[-0.02em]">Import existing sites</h1>
        <p className="mt-1 text-ink-muted">
          Reads what nginx, Caddy, Apache or Nginx Proxy Manager is already serving on this server and turns it into
          domains Islet routes. Nothing is written until you choose.
        </p>
      </div>

      <Steps step={step} />

      {error && <Alert>{error}</Alert>}

      {step === "scan" && (
        <>
          <Card
            title="What is on this server"
            description={scanning ? "Looking through the usual places…" : total > 0
              ? `${total} site${total === 1 ? "" : "s"} found.`
              : "Nothing found in the usual places. Paste a configuration below instead."}
          >
            {!scanning && found.length === 0 && (
              <p className="text-sm text-ink-muted">
                Looked in nginx's sites-enabled and conf.d, Caddy's Caddyfile, Apache's sites-enabled, and the data
                directories Nginx Proxy Manager uses in Docker. If your proxy keeps its files somewhere else, paste one
                below and it will be read the same way.
              </p>
            )}

            {found.map((f) => (
              <div key={f.source} className="border-b border-border py-3 first:pt-0 last:border-0 last:pb-0">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="text-sm font-medium">{f.source}</span>
                  <span className="text-xs text-ink-muted">
                    {f.sites.length} site{f.sites.length === 1 ? "" : "s"} in {f.files.length} file{f.files.length === 1 ? "" : "s"}
                  </span>
                </div>
                <div className="mt-1.5 space-y-1">
                  {f.sites.slice(0, 12).map((site, i) => (
                    <div key={i} className="flex flex-wrap items-baseline gap-x-2 text-xs">
                      <span className="font-medium">{site.hosts.join(", ")}</span>
                      <span className="font-mono text-ink-muted">
                        {site.upstream || site.root || site.rawUpstream || "nothing to forward to"}
                      </span>
                    </div>
                  ))}
                  {f.sites.length > 12 && (
                    <p className="text-xs text-ink-faint">and {f.sites.length - 12} more</p>
                  )}
                </div>
                <p className="mt-1.5 truncate font-mono text-[11px] text-ink-faint">{f.files.join("  ")}</p>
              </div>
            ))}

            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Button disabled={scanning || total === 0 || busy} onClick={() => void review("")}>
                Review {total > 0 ? `${total} site${total === 1 ? "" : "s"}` : "what was found"}
              </Button>
              <Button variant="secondary" className="gap-1.5" onClick={() => void scan()} disabled={scanning}>
                <RefreshIcon className={`h-4 w-4 ${scanning ? "animate-spin" : ""}`} />Scan again
              </Button>
            </div>
          </Card>

          <Card
            title="Or paste a configuration"
            description="An nginx server block, a Caddyfile, an Apache virtual host, or a file from Nginx Proxy Manager. Which one it is is worked out from the text."
          >
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={14}
              spellCheck={false}
              placeholder={"server {\n    server_name app.example.com;\n    location / {\n        proxy_pass http://127.0.0.1:3000;\n    }\n}"}
              className="w-full rounded-md border border-border-strong bg-bg p-3 font-mono text-xs text-ink placeholder:text-ink-faint focus:border-accent"
            />
            <div className="mt-2">
              <Button disabled={!text.trim() || busy} onClick={() => void review(text)}>Read it</Button>
            </div>
          </Card>
        </>
      )}

      {step === "review" && (
        <Card
          title="What will be imported"
          description="Each ticked name becomes a domain pointing at the same place it points at now. The other server keeps running until you stop it."
        >
          {importable.length === 0 && (
            <p className="text-sm text-ink-muted">Nothing here can be imported on its own. The notes below say why.</p>
          )}

          <div className="-mx-4 overflow-x-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead className="text-left text-xs text-ink-muted">
                <tr>
                  <th className="w-8 px-4 py-2"></th>
                  <th className="py-2 font-medium">Domain</th>
                  <th className="py-2 font-medium">Points at</th>
                  <th className="py-2 pr-4 font-medium">Notes</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {proposals.map((p, i) => (
                  <tr key={p.host + i} className={p.target ? "" : "opacity-60"}>
                    <td className="px-4 py-2">
                      <input
                        type="checkbox"
                        disabled={!p.target}
                        checked={chosen.has(p.host)}
                        onChange={(e) => setChosen((c) => {
                          const next = new Set(c);
                          if (e.target.checked) next.add(p.host); else next.delete(p.host);
                          return next;
                        })}
                        aria-label={`Import ${p.host}`}
                      />
                    </td>
                    <td className="py-2 font-medium">
                      {p.host}
                      {p.source && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">{p.source}</span>}
                    </td>
                    <td className="py-2 font-mono text-xs text-ink-muted">
                      <span className="block">{p.target || "—"}</span>
                      {/* Locations come across with the host, so they belong
                          on its row rather than in a place you have to go and
                          look. Seeing them is how somebody knows the import
                          understood their setup. */}
                      {(p.locations ?? []).map((l) => (
                        <span key={l.path} className="mt-0.5 block">
                          <span className="text-ink-faint">{l.path}</span>
                          {" → "}
                          {l.target || <span className="text-warning">?</span>}
                          {l.stripPath && <span className="ml-1 text-ink-faint" title="The path is removed before forwarding">(stripped)</span>}
                        </span>
                      ))}
                    </td>
                    <td className="max-w-[40ch] py-2 pr-4 text-xs text-ink-muted">
                      {p.note}
                      {(p.locations ?? []).filter((l) => l.note).map((l) => (
                        <span key={l.path} className="mt-0.5 block text-warning">{l.path}: {l.note}</span>
                      ))}
                      {(p.skipped ?? []).length > 0 && (
                        <span className="mt-0.5 block text-warning">
                          Not imported, because Islet has no equivalent: {(p.skipped ?? []).join(", ")}. Recreate these by hand if you need them.
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button disabled={chosen.size === 0 || busy} onClick={() => void apply()}>
              {busy ? "Importing…" : `Import ${chosen.size} domain${chosen.size === 1 ? "" : "s"}`}
            </Button>
            <Button variant="secondary" onClick={() => setStep("scan")}>Back</Button>
          </div>
        </Card>
      )}

      {step === "done" && (
        <Card title={`${savedCount} domain${savedCount === 1 ? "" : "s"} imported`} description="They are routed by Islet as soon as the old server stops answering on ports 80 and 443.">
          <ol className="list-decimal space-y-2 pl-5 text-sm">
            <li>
              Check the list on <Link to="/domains" className="text-accent hover:underline">Domains</Link>. Each imported
              name should say where it points and whether DNS reaches this server.
            </li>
            <li>
              Stop the old proxy, or move it off ports 80 and 443 — Islet's proxy needs them to answer and to get
              certificates. On a systemd server that is usually <code className="font-mono text-xs">systemctl disable --now nginx</code>.
            </li>
            <li>Reload one of the sites. If something is wrong, the old configuration is still on disk, untouched.</li>
          </ol>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button onClick={() => navigate("/domains")}>Go to Domains</Button>
            <Button variant="secondary" onClick={() => { setStep("scan"); setProposals([]); setText(""); void scan(); }}>
              Import more
            </Button>
          </div>
        </Card>
      )}
    </div>
  );
}

function Steps({ step }: { step: Step }) {
  const order: Step[] = ["scan", "review", "done"];
  const labels: Record<Step, string> = { scan: "Find", review: "Choose", done: "Finish" };
  const at = order.indexOf(step);
  return (
    <ol className="flex flex-wrap gap-1.5 text-[11px]">
      {order.map((s, i) => (
        <li
          key={s}
          className={`rounded-sm border px-1.5 py-0.5 ${
            i < at ? "border-success/40 text-success"
              : i === at ? "border-accent/40 text-accent"
                : "border-border text-ink-faint"
          }`}
        >
          {labels[s]}
        </li>
      ))}
    </ol>
  );
}
