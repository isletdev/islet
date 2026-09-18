import { useEffect, useState } from "react";
import { api, RequestError } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { whereNext, type ReturnTo } from "@/lib/returnto";
import { Alert, AuthFrame, Button } from "@/components/ui";

/**
 * Somebody already signed in, sent here by a protected site.
 *
 * This is the case that was missing. A protected host asks forward-auth, gets
 * "not you", and sends the visitor to the panel with `?next=`. If they were not
 * signed in they reached the login form, which knew what to do with it. If they
 * were — which is the ordinary case, since they are an admin with the panel
 * open in another tab — the panel had no route for /login, so the catch-all
 * sent them to the dashboard and the address they asked for was dropped on the
 * floor. The protected site then looked like a link to the Islet panel.
 */
export default function Returning() {
  const { state } = useAuth();
  const [to, setTo] = useState<ReturnTo | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    void whereNext().then((r) => {
      if (r.kind === "go") location.replace(r.url);
      else setTo(r);
    });
  }, []);

  if (!to || to.kind === "go") {
    return <AuthFrame title="Taking you back"><p className="text-sm text-ink-muted">One moment.</p></AuthFrame>;
  }
  if (to.kind === "none") {
    location.replace("/");
    return null;
  }

  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const fix = async () => {
    setErr(null);
    setBusy(true);
    try {
      // The daemon re-issues this very session at the new scope in its reply,
      // so by the time this returns the browser already holds a cookie the
      // protected host will be sent. Carry on to where they were going.
      await api.cookieDomainSet(to.suggest);
      const again = await whereNext();
      location.replace(again.kind === "go" ? again.url : "/");
    } catch (e) {
      setErr(e instanceof RequestError ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <AuthFrame title={`${to.host} cannot see this session`}>
      <div className="space-y-4">
        <Alert tone="warning">{to.why}</Alert>
        {to.suggest && isAdmin ? (
          <>
            <p className="text-sm text-ink-muted">
              Both names sit under <code className="font-mono">{to.suggest}</code>. Scoping the panel's session
              cookie there lets a sign-in here count at <code className="font-mono">{to.host}</code> — and at every
              other host under <code className="font-mono">{to.suggest}</code>, which is the part worth deciding
              deliberately: any site under that name will be handed the cookie that proves who is visiting. It is not the panel's own session — that stays on this host — but it does say who you are.
            </p>
            {err && <Alert>{err}</Alert>}
            <Button onClick={() => void fix()} disabled={busy} className="w-full">
              {busy ? "Saving…" : `Scope sessions to ${to.suggest} and continue`}
            </Button>
          </>
        ) : (
          <p className="text-sm text-ink-muted">
            {to.suggest
              ? `An admin can set the session cookie domain to ${to.suggest} under Settings, Team, "Protect apps with Islet login".`
              : "These two names share no parent domain a cookie may be scoped to, so an Islet login cannot protect that host."}
          </p>
        )}
        <a href="/" className="block text-center text-sm text-ink-muted underline hover:text-ink">Go to the panel instead</a>
      </div>
    </AuthFrame>
  );
}
