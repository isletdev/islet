import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { api, onSessionExpired, RequestError, type Me } from "./api";

type State =
  | { status: "loading" }
  | { status: "setup" }
  | { status: "anonymous" }
  | { status: "mfa" }
  | { status: "authed"; me: Me };

interface AuthContext {
  state: State;
  refresh: () => Promise<void>;
  signOut: () => Promise<void>;
}

const Ctx = createContext<AuthContext | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>({ status: "loading" });

  const refresh = useCallback(async () => {
    try {
      const me = await api.me();
      setState({ status: "authed", me });
      return;
    } catch (e) {
      if (e instanceof RequestError && e.body.error === "mfa_required") {
        setState({ status: "mfa" });
        return;
      }
      if (!(e instanceof RequestError) || e.status !== 401) {
        setState({ status: "anonymous" });
        return;
      }
    }
    try {
      const s = await api.setupStatus();
      setState(s.needsSetup ? { status: "setup" } : { status: "anonymous" });
    } catch {
      setState({ status: "anonymous" });
    }
  }, []);

  const signOut = useCallback(async () => {
    try { await api.logout(); } catch { /* cookie is cleared server-side regardless */ }
    setState({ status: "anonymous" });
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  // A 401 from any call means the session is gone, so show the sign-in screen
  // rather than a page full of numbers that stopped being true.
  useEffect(() => {
    onSessionExpired(() => setState({ status: "anonymous" }));
  }, []);

  return <Ctx.Provider value={{ state, refresh, signOut }}>{children}</Ctx.Provider>;
}

export function useAuth() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useAuth outside AuthProvider");
  return c;
}
