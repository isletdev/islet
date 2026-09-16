import { useState, type FormEvent } from "react";
import { api, RequestError } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { t } from "@/lib/i18n";
import { whereNext } from "@/lib/returnto";
import { Alert, AuthFrame, Button, Field, Input } from "@/components/ui";

/**
 * After forward-auth sends someone here, go back to the site they asked for.
 *
 * The decision lives in lib/returnto so that this page and the already-signed-in
 * path answer it the same way — they used to differ, and the difference was the
 * bug: signed out you were returned, signed in you were dropped on the
 * dashboard.
 *
 * Returns a sentence when it cannot go. Landing on the dashboard with no
 * explanation reads as "the login failed", when the login in fact worked and
 * the problem is that these two names can never share a cookie.
 */
async function followNext(): Promise<string | null> {
  const to = await whereNext();
  if (to.kind === "go") {
    location.replace(to.url);
    return null;
  }
  if (to.kind === "blocked") return to.why;
  return null;
}

export default function Login({ mfa = false }: { mfa?: boolean }) {
  const { refresh, signOut } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submitLogin(e: FormEvent) {
    e.preventDefault();
    setError(null); setBusy(true);
    try {
      await api.login(username.trim(), password);
      const why = await followNext();
      if (why) { setError(why); return; }
      await refresh();
    } catch (err) {
      if (err instanceof RequestError && err.status === 429) setError("Too many attempts. Wait a few minutes and try again.");
      else setError(err instanceof RequestError ? err.message : "Sign-in failed.");
    } finally { setBusy(false); }
  }

  async function submitCode(e: FormEvent) {
    e.preventDefault();
    setError(null); setBusy(true);
    try {
      await api.mfa(code);
      await refresh();
      followNext();
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Verification failed.");
    } finally { setBusy(false); }
  }

  if (mfa) {
    return (
      <AuthFrame title={t("login.mfa.title")} subtitle="Enter the 6-digit code from your authenticator app, or a recovery code.">
        <form onSubmit={submitCode} className="space-y-4">
          {error && <Alert>{error}</Alert>}
          <Field label={t("login.code")}>
            <Input value={code} onChange={(e) => setCode(e.target.value)} required autoFocus autoComplete="one-time-code" inputMode="numeric" className="font-mono tracking-widest" placeholder="123456" />
          </Field>
          <Button type="submit" className="w-full" disabled={busy}>{busy ? "Checking…" : "Continue"}</Button>
          <button type="button" onClick={() => void signOut()} className="w-full text-center text-sm text-ink-muted hover:text-ink">{t("login.other")}</button>
        </form>
      </AuthFrame>
    );
  }

  return (
    <AuthFrame title={t("login.title")}>
      <form onSubmit={submitLogin} className="space-y-4">
        {error && <Alert>{error}</Alert>}
        <Field label={t("login.username")}>
          <Input value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus autoComplete="username" />
        </Field>
        <Field label={t("login.password")}>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required autoComplete="current-password" />
        </Field>
        <Button type="submit" className="w-full" disabled={busy}>{busy ? t("login.busy") : t("login.submit")}</Button>
      </form>
    </AuthFrame>
  );
}
