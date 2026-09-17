import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, RequestError, type VaultSecret } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Input } from "@/components/ui";
import Modal from "@/components/Modal";
import { useDialog } from "@/lib/dialogs";

function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }
function when(s?: string) { return s ? new Date(s).toLocaleString() : "never"; }

/**
 * Secrets under a name, so the same value is not pasted into three places and
 * rotated in one.
 *
 * A value goes in and does not come back out: the list carries names and
 * nothing else, and revealing one is an admin action that is written to the
 * audit log every time. What a secret is *for* is referring to it as
 * @vault:NAME somewhere that runs.
 */
export default function Vault() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const ask = useDialog();
  const [list, setList] = useState<VaultSecret[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [desc, setDesc] = useState("");
  const [shown, setShown] = useState<{ name: string; value: string } | null>(null);

  const load = useCallback(() => {
    api.vault().then((l) => { setList(l); setError(null); }).catch((e) => setError(err(e)));
  }, []);
  useEffect(load, [load]);

  const save = async (e: FormEvent) => {
    e.preventDefault(); setError(null);
    try {
      await api.vaultSet({ name: name.trim().toUpperCase(), value, description: desc.trim() });
      setName(""); setValue(""); setDesc(""); load();
    } catch (e2) { setError(err(e2)); }
  };

  const reveal = async (n: string) => {
    if (!(await ask.confirm({
      title: `Show ${n}?`,
      body: <>The value appears on this screen, and the fact that you looked is written to the audit log.</>,
      confirmLabel: "Show it",
    }))) return;
    try { setShown(await api.vaultReveal(n)); } catch (e2) { setError(err(e2)); }
  };

  const remove = async (n: string) => {
    if (!(await ask.confirm({
      title: `Delete ${n}?`,
      body: <>Anything still referring to <span className="font-mono">@vault:{n}</span> will stop working. This cannot be undone.</>,
      confirmLabel: "Delete", tone: "danger",
    }))) return;
    try { await api.vaultDelete(n); if (shown?.name === n) setShown(null); load(); } catch (e2) { setError(err(e2)); }
  };

  return (
    <div className="mx-auto max-w-4xl space-y-3 sm:space-y-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Vault</h1>
        <p className="mt-1 hidden text-ink-muted sm:block">
          Store a secret once under a name, then write <span className="font-mono text-ink">@vault:NAME</span> wherever it is needed —
          an app's environment, a cron command. Rotating it is one edit here.
        </p>
      </div>

      {error && <Alert>{error}</Alert>}
      {shown && <Revealed name={shown.name} value={shown.value} onClose={() => setShown(null)} />}

      <Card title="Secrets" description="Names only. A value is never sent back to this page unless you ask for it.">
        <ul className="divide-y divide-border">
          {list?.map((v) => (
            <li key={v.id} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
              <div className="min-w-0">
                <div className="font-mono font-medium">{v.name}</div>
                <div className="text-xs text-ink-muted">
                  {v.description || "no description"} · updated {when(v.updatedAt)} · last used {when(v.lastUsedAt)}
                </div>
              </div>
              <div className="flex gap-1.5">
                {isAdmin && <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => void reveal(v.name)}>Show</Button>}
                <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => void remove(v.name)}>Delete</Button>
              </div>
            </li>
          ))}
          {list?.length === 0 && (
            <li className="py-6 text-center text-sm text-ink-muted">
              Nothing stored yet. A database password is a good first one.
            </li>
          )}
          {!list && <li className="py-6 text-center text-sm text-ink-muted">Loading…</li>}
        </ul>
      </Card>

      <Card title="Add or replace" description="Storing under a name that already exists rotates it; everything referring to the name picks the new value up.">
        <form onSubmit={save} className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="DATABASE_PASSWORD" className="font-mono" required aria-label="Name" />
          <Input type="password" value={value} onChange={(e) => setValue(e.target.value)} placeholder="the secret" className="font-mono" required aria-label="Value" />
          <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="what it is for" aria-label="Description" />
          <Button type="submit" className="h-9 text-xs">Store</Button>
        </form>
      </Card>
    </div>
  );
}

/**
 * A revealed secret, on top of the page rather than pushed into a banner above
 * it.
 *
 * It used to be an alert at the top of the list: the value wrapped across the
 * width of the page, a long one pushed everything else down, and on a phone
 * somebody had to scroll up to find what they had just asked for. It is also
 * the one thing on this page nobody wants left on screen, so it closes with
 * Escape, with a button, and takes the value to the clipboard on the way out if
 * that is what was wanted.
 */
function Revealed({ name, value, onClose }: { name: string; value: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1500); }
    catch { /* a browser that refuses the clipboard still shows the value */ }
  };
  return (
    <Modal title={name} description="On screen until you close it. Revealing a secret is in the audit log." onClose={onClose}>
      <pre className="max-h-64 overflow-auto rounded-md border border-border bg-bg p-3 font-mono text-sm break-all whitespace-pre-wrap">{value}</pre>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button type="button" onClick={() => void copy()}>{copied ? "Copied" : "Copy"}</Button>
        <Button type="button" variant="secondary" onClick={onClose}>Close</Button>
      </div>
    </Modal>
  );
}
