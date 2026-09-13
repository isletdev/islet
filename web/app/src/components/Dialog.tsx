import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Button, Input } from "@/components/ui";

/**
 * The panel's own dialog.
 *
 * It replaces window.confirm, window.prompt and window.alert, which look like a
 * browser error rather than part of the product, cannot say which of two
 * outcomes is the dangerous one, and do not work at all inside the embedded app
 * frames the panel renders.
 */

export type DialogTone = "default" | "danger";

export interface DialogRequest {
  kind: "confirm" | "prompt" | "alert";
  title: string;
  body?: ReactNode;
  /** Label on the action that proceeds. */
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: DialogTone;
  /** prompt only */
  label?: string;
  defaultValue?: string;
  placeholder?: string;
  mono?: boolean;
  /** Require this exact word before the action is allowed. */
  typeToConfirm?: string;
}

export default function Dialog({ req, onDone }: { req: DialogRequest; onDone: (v: string | boolean | null) => void }) {
  const [value, setValue] = useState(req.defaultValue ?? "");
  const [typed, setTyped] = useState("");
  const box = useRef<HTMLDivElement>(null);
  const first = useRef<HTMLInputElement>(null);
  const go = useRef<HTMLButtonElement>(null);
  const titleId = useId();
  const danger = req.tone === "danger";
  const ready = !req.typeToConfirm || typed === req.typeToConfirm;

  const cancel = () => onDone(req.kind === "prompt" ? null : false);
  const accept = () => {
    if (!ready) return;
    onDone(req.kind === "prompt" ? value : true);
  };

  // Escape closes, and Tab stays inside: a modal that leaks focus to the page
  // behind it is worse than no modal.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); cancel(); return; }
      if (e.key !== "Tab" || !box.current) return;
      const items = box.current.querySelectorAll<HTMLElement>("button, input, [href], select, textarea");
      if (items.length === 0) return;
      const firstEl = items[0], lastEl = items[items.length - 1];
      if (!e.shiftKey && document.activeElement === lastEl) { e.preventDefault(); firstEl.focus(); }
      if (e.shiftKey && document.activeElement === firstEl) { e.preventDefault(); lastEl.focus(); }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  // Focus what the person is going to use. For a destructive action that is not
  // the destructive button.
  useEffect(() => {
    const t = setTimeout(() => (first.current ?? go.current)?.focus(), 0);
    return () => clearTimeout(t);
  }, []);

  return (
    <div
      className="fixed inset-0 z-[60] flex items-start justify-center bg-black/40 p-4 pt-[14vh]"
      onMouseDown={(e) => { if (e.target === e.currentTarget) cancel(); }}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
    >
      <div ref={box} className="w-full max-w-md overflow-hidden rounded-lg border border-border bg-surface shadow-float">
        <form
          onSubmit={(e) => { e.preventDefault(); accept(); }}
          className="px-5 py-4"
        >
          <h2 id={titleId} className="font-semibold">{req.title}</h2>
          {req.body && <div className="mt-1.5 text-sm text-ink-muted">{req.body}</div>}

          {req.kind === "prompt" && (
            <label className="mt-4 block">
              {req.label && <span className="mb-1.5 block h-5 text-sm font-medium leading-5">{req.label}</span>}
              <Input
                ref={first}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={req.placeholder}
                className={req.mono ? "font-mono" : ""}
                autoComplete="off"
                spellCheck={false}
              />
            </label>
          )}

          {req.typeToConfirm && (
            <label className="mt-4 block">
              <span className="mb-1.5 block h-5 text-sm font-medium leading-5">
                Type <span className="font-mono">{req.typeToConfirm}</span> to confirm
              </span>
              <Input
                ref={req.kind === "prompt" ? undefined : first}
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </label>
          )}

          <div className="mt-5 flex items-center justify-end gap-2">
            {req.kind !== "alert" && (
              <Button type="button" variant="secondary" onClick={cancel}>{req.cancelLabel ?? "Cancel"}</Button>
            )}
            <Button ref={go} type="submit" variant={danger ? "danger" : "primary"} disabled={!ready}>
              {req.confirmLabel ?? (req.kind === "alert" ? "Close" : "OK")}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
