import { useEffect, useState, type FormEvent } from "react";
import QRCode from "qrcode";
import { api, RequestError, type Session } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";

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
