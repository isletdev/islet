import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, RequestError, setServer, type FleetServer } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useDialog } from "@/lib/dialogs";
import { pollInterval } from "@/lib/poll";
import { Alert, Button, Card, Field, FieldAction, Input, Select } from "@/components/ui";
import { ExternalIcon, RefreshIcon, TrashIcon } from "@/components/icons";

function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }

/**
 * The servers this panel manages.
 *
 * Adding one is a form and a progress list. Everything else about a server is
 * reached by switching to it in the header, which points every other page at
 * that machine, so there is nothing to learn twice.
 */
export default function Servers() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const ask = useDialog();
  const [local, setLocal] = useState<{ name: string; hostname: string; version: string } | null>(null);
  const [list, setList] = useState<FleetServer[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [joining, setJoining] = useState<FleetServer | null>(null);
  const [exposed, setExposed] = useState<Record<string, boolean>>({});

  const load = useCallback(() => api.servers()
    .then((r) => { setLocal(r.local); setList(r.servers); setError(null); })
    .catch((e) => setError(err(e))), []);

  useEffect(() => { void load(); const stop = pollInterval(() => void load(), 15000); return stop; }, [load]);

  // Whether each server's panel port answers from here. It is checked once per
  // visit rather than on every poll: it opens a socket to another machine.
  useEffect(() => {
    let alive = true;
    for (const s of list) {
      if (s.status !== "ready" || s.id in exposed) continue;
      void api.serverExposure(s.id)
        .then((r) => alive && setExposed((e) => ({ ...e, [s.id]: r.panelOpen })))
        .catch(() => {});
    }
    return () => { alive = false; };
  }, [list, exposed]);

  const closePanel = async (s: FleetServer) => {
    const ok = await ask.confirm({
      title: `Close port ${s.panelPort} on ${s.name}?`,
      body: "This panel reaches it over SSH, so nothing here stops working. You will no longer be able to open that server's own panel in a browser directly.",
      confirmLabel: "Close the port",
      tone: "danger",
    });
    if (!ok) return;
    try {
      await api.serverClosePanel(s.id, s.panelPort);
      setExposed((e) => ({ ...e, [s.id]: false }));
    } catch (e) {
      void ask.alert({ title: "Could not close it", body: err(e), tone: "danger" });
    }
  };

  const forget = async (s: FleetServer) => {
    const ok = await ask.confirm({
      title: `Stop managing ${s.name}?`,
      body: "It keeps running and keeps serving. This panel simply stops listing it. To take this panel's access away as well, remove its key from that server's authorized_keys.",
      confirmLabel: "Stop managing",
      tone: "danger",
    });
    if (!ok) return;
    try { await api.serverForget(s.id); await load(); } catch (e) { void ask.alert({ title: "Could not remove it", body: err(e), tone: "danger" }); }
  };

  const check = async (s: FleetServer) => {
    try {
      const r = await api.serverCheck(s.id);
      if (!r.ok) void ask.alert({ title: `${s.name} did not answer`, body: r.error ?? "", tone: "danger" });
      await load();
    } catch (e) { void ask.alert({ title: "Check failed", body: err(e), tone: "danger" }); }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Servers</h1>
          <p className="mt-1 text-ink-muted">Every machine this panel manages. Adding one installs Islet on it over SSH and connects it here.</p>
        </div>
        {isAdmin && !adding && !joining && <Button onClick={() => setAdding(true)}>Add a server</Button>}
      </div>

      {error && <Alert>{error}</Alert>}

      {adding && <AddServer onCancel={() => setAdding(false)} onAdded={async (s) => { setAdding(false); await load(); setJoining(s); }} />}
      {joining && <JoinWizard server={joining} onClose={async () => { setJoining(null); await load(); }} onStarted={() => void load()} />}

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {local && (
          <div className="rounded-lg border border-border bg-surface p-4">
            <div className="flex items-center justify-between gap-2">
              <span className="font-semibold">{local.name}</span>
              <span className="rounded-sm bg-surface-2 px-1.5 py-0.5 text-[11px] text-ink-muted">this panel</span>
            </div>
            <p className="mt-1 text-sm text-ink-muted">{local.hostname}</p>
            <p className="mt-2 font-mono text-[11px] text-ink-faint">{local.version}</p>
          </div>
        )}

        {list.map((s) => (
          <div key={s.id} className="rounded-lg border border-border bg-surface p-4">
            <div className="flex items-center justify-between gap-2">
              <span className="min-w-0 truncate font-semibold">{s.name}</span>
              <StatusPill s={s} />
            </div>
            <p className="mt-1 truncate text-sm text-ink-muted">{s.sshUser}@{s.host}{s.sshPort !== 22 ? `:${s.sshPort}` : ""}{s.hostname ? ` · ${s.hostname}` : ""}</p>
            {s.statusNote && <p className="mt-2 text-xs text-danger">{s.statusNote}</p>}
            {exposed[s.id] && (
              <p className="mt-2 flex flex-wrap items-center gap-2 rounded-md bg-warning-soft px-2 py-1.5 text-xs text-warning">
                <span>Its panel is open on port {s.panelPort}. This one reaches it over SSH, so it does not need to be.</span>
                <button type="button" onClick={() => void closePanel(s)} className="font-medium underline underline-offset-2">Close it</button>
              </p>
            )}
            <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
              {s.status === "ready" && (
                <Button variant="secondary" className="h-7 gap-1.5 px-2 text-xs" onClick={() => setServer(s.id)}>
                  <ExternalIcon className="h-3.5 w-3.5" />Open
                </Button>
              )}
              {(s.status === "pending" || s.status === "failed") && isAdmin && (
                <Button className="h-7 px-2 text-xs" onClick={() => setJoining(s)}>
                  {s.status === "failed" ? "Try again" : "Set it up"}
                </Button>
              )}
              {s.status === "joining" && <Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => setJoining(s)}>Watch progress</Button>}
              {s.status === "ready" && <Button variant="secondary" className="h-7 gap-1.5 px-2 text-xs" onClick={() => void check(s)}><RefreshIcon className="h-3.5 w-3.5" />Check</Button>}
              {isAdmin && <Button variant="danger" className="h-7 gap-1.5 px-2 text-xs" onClick={() => void forget(s)}><TrashIcon className="h-3.5 w-3.5" />Remove</Button>}
              {s.version && <span className="ml-auto font-mono text-[11px] text-ink-faint">{s.version}</span>}
            </div>
          </div>
        ))}
      </div>

      {list.length === 0 && !error && !adding && !joining && (
        <div className="rounded-lg border border-dashed border-border p-8 text-center">
          <p className="text-sm text-ink-muted">No other servers yet. Adding one installs Islet on it and brings it under this login.</p>
          {isAdmin && <Button className="mt-3" onClick={() => setAdding(true)}>Add a server</Button>}
        </div>
      )}
    </div>
  );
}

function StatusPill({ s }: { s: FleetServer }) {
  const look = {
    ready: "bg-success-soft text-success",
    joining: "bg-accent-soft text-accent",
    pending: "bg-surface-2 text-ink-muted",
    failed: "bg-danger-soft text-danger",
    unreachable: "bg-warning-soft text-warning",
  }[s.status] ?? "bg-surface-2 text-ink-muted";
  const word = { ready: "ready", joining: "setting up", pending: "not set up", failed: "failed", unreachable: "unreachable" }[s.status] ?? s.status;
  return <span className={`shrink-0 rounded-sm px-1.5 py-0.5 text-[11px] ${look}`}>{word}</span>;
}

function AddServer({ onCancel, onAdded }: { onCancel: () => void; onAdded: (s: FleetServer) => void }) {
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [sshUser, setUser] = useState("root");
  const [sshPort, setPort] = useState("22");
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setMsg(null);
    try { onAdded(await api.serverAdd({ name, host, sshUser, sshPort: +sshPort })); }
    catch (er) { setMsg(err(er)); } finally { setBusy(false); }
  };

  return (
    <Card title="Add a server" description="Where it is. Nothing is installed yet; the next step asks how to log in once.">
      <form onSubmit={submit} className="flex flex-wrap items-start gap-3">
        <Field label="Name" hint="What you will call it here." className="w-44"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="web-2" required /></Field>
        <Field label="Address" hint="Host name or IP." className="w-56"><Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="203.0.113.8" className="font-mono" required /></Field>
        <Field label="SSH user" className="w-28"><Input value={sshUser} onChange={(e) => setUser(e.target.value)} className="font-mono" /></Field>
        <Field label="SSH port" className="w-20"><Input value={sshPort} onChange={(e) => setPort(e.target.value)} className="font-mono" /></Field>
        <FieldAction className="flex items-center gap-2">
          <Button type="submit" disabled={busy}>{busy ? "Adding…" : "Continue"}</Button>
          <Button type="button" variant="secondary" onClick={onCancel}>Cancel</Button>
          {msg && <span className="text-xs text-danger">{msg}</span>}
        </FieldAction>
      </form>
    </Card>
  );
}

const STEPS: Record<string, string> = {
  connect: "Connecting",
  check: "Checking the server",
  key: "Installing this panel's key",
  install: "Installing Islet",
  token: "Getting a token",
  verify: "Verifying",
};

/**
 * The join, as it happens.
 *
 * The credentials here are used once. The panel installs its own key during the
 * join, so this is the only time a password is typed, and it is never stored.
 */
function JoinWizard({ server, onClose, onStarted }: { server: FleetServer; onClose: () => void; onStarted: () => void }) {
  const [how, setHow] = useState<"password" | "key">("password");
  const [password, setPassword] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [panelKey, setPanelKey] = useState("");
  // The log view appears as soon as there is anything to show, whether this
  // wizard started the run or is looking at one that finished while it was
  // closed. `retry` is the one thing that sends it back to the form.
  const [started, setStarted] = useState(false);
  const [retry, setRetry] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  const [at, setAt] = useState("connect");
  const [done, setDone] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const box = useRef<HTMLPreElement>(null);

  useEffect(() => { void api.serverKey().then((r) => setPanelKey(r.publicKey)).catch(() => {}); }, []);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [lines]);

  // Follow the install. Reconnecting replays it, so closing this and coming
  // back loses nothing.
  useEffect(() => {
    if (retry) return;
    const es = new EventSource(`/api/v1/servers/${server.id}/join/events`);
    es.addEventListener("progress", (ev) => {
      const p = JSON.parse((ev as MessageEvent).data) as { step: string; line?: string; error?: string; done?: boolean };
      // The marker stays on the step that failed rather than running to the end.
      setStarted(true);
      setAt(p.step);
      if (p.error) { setFailed(p.error); setLines((l) => [...l.slice(-400), "error: " + p.error]); }
      if (p.done) setDone(true);
      if (p.line) setLines((l) => [...l.slice(-400), p.line as string]);
    });
    es.addEventListener("end", () => es.close());
    return () => es.close();
  }, [retry, server.id]);

  const start = async (e: FormEvent) => {
    e.preventDefault(); setMsg(null);
    try {
      await api.serverJoin(server.id, {
        user: server.sshUser,
        password: how === "password" ? password : "",
        privateKey: how === "key" ? privateKey : "",
        passphrase: how === "key" ? passphrase : "",
      });
      setPassword(""); setPrivateKey(""); setPassphrase("");
      setLines([]); setAt("connect"); setFailed(null); setRetry(false);
      setStarted(true);
      onStarted();
    } catch (er) { setMsg(err(er)); }
  };

  return (
    <Card
      title={`Set up ${server.name}`}
      description={`${server.sshUser}@${server.host}. Islet will be installed here and connected to this panel.`}
    >
      {!started || retry ? (
        <form onSubmit={start} className="space-y-4">
          <p className="text-sm text-ink-muted">
            This is used once, to log in. During setup the panel installs its own key and
            what you type here is never stored.
          </p>
          <div className="flex flex-wrap items-start gap-3">
            <Field label="How to log in" className="w-44">
              <Select value={how} onChange={(e) => setHow(e.target.value as "password" | "key")}>
                <option value="password">Password</option>
                <option value="key">Private key</option>
              </Select>
            </Field>
            {how === "password" ? (
              <Field label="Root password" className="w-64"><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" required /></Field>
            ) : (
              <Field label="Passphrase" hint="Only if the key has one." className="w-52"><Input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} autoComplete="off" /></Field>
            )}
          </div>
          {how === "key" && (
            <Field label="Private key">
              <textarea value={privateKey} onChange={(e) => setPrivateKey(e.target.value)} rows={5} spellCheck={false} required
                className="w-full rounded-md border border-border-strong bg-bg p-2 font-mono text-xs" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
            </Field>
          )}
          <details className="rounded-md border border-border p-3">
            <summary className="cursor-pointer text-sm text-ink-muted hover:text-ink">Rather not type a password? Install this panel's key yourself.</summary>
            <p className="mt-2 text-xs text-ink-muted">Add this line to <span className="font-mono">~/.ssh/authorized_keys</span> on that server, then choose Private key above and paste any key that already works, or run the setup again once it is in place.</p>
            <pre className="mt-2 overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-[11px]">{panelKey || "…"}</pre>
          </details>
          <div className="flex items-center gap-2">
            <Button type="submit">Install and connect</Button>
            <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
            {msg && <span className="text-sm text-danger">{msg}</span>}
          </div>
        </form>
      ) : (
        <div className="space-y-3">
          <ol className="flex flex-wrap gap-1.5 text-[11px]">
            {Object.entries(STEPS).map(([id, label]) => {
              const order = Object.keys(STEPS);
              const here = order.indexOf(at), mine = order.indexOf(id);
              const state = done ? "border-success/40 text-success"
                : failed && mine === here ? "border-danger/40 text-danger"
                : mine < here ? "border-success/40 text-success"
                : mine === here ? "border-accent/40 text-accent"
                : "border-border text-ink-faint";
              return <li key={id} className={`rounded-sm border px-1.5 py-0.5 ${state}`}>{label}</li>;
            })}
          </ol>
          <pre ref={box} className="max-h-72 overflow-auto rounded-md border border-border bg-code-bg p-3 font-mono text-xs whitespace-pre-wrap text-code-fg">
            {lines.join("\n") || "Starting…"}
          </pre>
          {done && <Alert tone="success">{server.name} is ready. Switch to it from the header, or open it from the list.</Alert>}
          {failed && <Alert>{failed}</Alert>}
          <div className="flex items-center gap-2">
            <Button variant="secondary" onClick={onClose}>{done || failed ? "Close" : "Run in the background"}</Button>
            {failed && <Button onClick={() => { setRetry(true); setStarted(false); setFailed(null); setLines([]); setAt("connect"); }}>Try again</Button>}
            {!done && !failed && <span className="text-xs text-ink-muted">You can close this. The install keeps going.</span>}
          </div>
        </div>
      )}
    </Card>
  );
}
