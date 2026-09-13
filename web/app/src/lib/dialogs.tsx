import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import Dialog, { type DialogRequest } from "@/components/Dialog";

/**
 * Asking the person something, without the browser's own dialogs.
 *
 * The calls return promises so a handler still reads top to bottom:
 *
 *   if (!(await ask.confirm({ title: "Delete it?", tone: "danger" }))) return;
 *
 * One dialog is open at a time, which is the same rule the browser enforced.
 */

type Opts = Omit<DialogRequest, "kind">;

interface Asker {
  confirm(o: Opts): Promise<boolean>;
  prompt(o: Opts): Promise<string | null>;
  alert(o: Opts): Promise<void>;
}

const Ctx = createContext<Asker | null>(null);

export function DialogProvider({ children }: { children: ReactNode }) {
  const [req, setReq] = useState<DialogRequest | null>(null);
  const resolve = useRef<((v: string | boolean | null) => void) | null>(null);

  const open = useCallback((r: DialogRequest) => {
    // A second question while one is open would strand the first promise.
    resolve.current?.(r.kind === "prompt" ? null : false);
    setReq(r);
    return new Promise<string | boolean | null>((res) => { resolve.current = res; });
  }, []);

  const asker = useMemo<Asker>(() => ({
    confirm: (o) => open({ ...o, kind: "confirm" }).then((v) => v === true),
    prompt: (o) => open({ ...o, kind: "prompt" }).then((v) => (typeof v === "string" ? v : null)),
    alert: (o) => open({ ...o, kind: "alert" }).then(() => undefined),
  }), [open]);

  const done = (v: string | boolean | null) => {
    setReq(null);
    const r = resolve.current;
    resolve.current = null;
    r?.(v);
  };

  return (
    <Ctx.Provider value={asker}>
      {children}
      {req && <Dialog req={req} onDone={done} />}
    </Ctx.Provider>
  );
}

export function useDialog(): Asker {
  const c = useContext(Ctx);
  if (!c) throw new Error("useDialog outside DialogProvider");
  return c;
}

/** Turn an unknown failure into something worth reading in a dialog. */
export function failure(e: unknown): string {
  if (e && typeof e === "object" && "message" in e) return String((e as { message: unknown }).message);
  return String(e);
}
