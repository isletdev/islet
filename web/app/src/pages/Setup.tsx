import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api, RequestError } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, AuthFrame, Button, Field, Input } from "@/components/ui";

export default function Setup() {
  const [params] = useSearchParams();
  const { refresh } = useAuth();
  const [token, setToken] = useState(params.get("token") ?? "");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) { setError("Passwords do not match."); return; }
    setBusy(true);
    try {
      await api.setup(token.trim(), username.trim(), password);
      await refresh();
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Setup failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthFrame title="Create the admin account" subtitle="This server has no users yet. The setup token comes from the installer output or the daemon log.">
      <form onSubmit={submit} className="space-y-4">
        {error && <Alert>{error}</Alert>}
        <Field label="Setup token" hint="Found in the installer output, or in the log: journalctl -u isletd">
          <Input value={token} onChange={(e) => setToken(e.target.value)} required autoComplete="off" spellCheck={false} className="font-mono text-xs" />
        </Field>
        <Field label="Username" hint="Lowercase letters, digits, dot, dash or underscore. 3 to 32 characters.">
          <Input value={username} onChange={(e) => setUsername(e.target.value)} required autoComplete="username" />
        </Field>
        <Field label="Password" hint="At least 12 characters. A long phrase beats a short jumble.">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={12} autoComplete="new-password" />
        </Field>
        <Field label="Confirm password">
          <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required minLength={12} autoComplete="new-password" />
        </Field>
        <Button type="submit" className="w-full" disabled={busy}>{busy ? "Creating account…" : "Create account and sign in"}</Button>
        <p className="text-xs text-ink-muted">You will be asked to enable two-factor authentication next. Do it: this account controls the whole server.</p>
      </form>
    </AuthFrame>
  );
}
