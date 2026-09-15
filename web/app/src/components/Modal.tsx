import { useEffect, useId, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";

/**
 * A form on top of the page, rather than at the bottom of it.
 *
 * Editing used to append a card below whatever was on screen, which worked
 * until the thing above it was a terminal: xterm draws into positioned canvases
 * inside its own stacking context, and on the workspaces page — where the
 * terminal takes the height that is left — the two overlapped and rows of the
 * terminal appeared through the form.
 *
 * Two details make that impossible rather than unlikely. It renders into
 * document.body through a portal, so no ancestor's transform, filter or
 * z-index can trap it; and it sits below the z-index of the confirm and prompt
 * dialogs, so a "delete this?" raised from inside a form still lands on top of
 * the form that raised it.
 */
export default function Modal({
  title,
  description,
  onClose,
  children,
  wide,
}: {
  title: string;
  description?: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  const box = useRef<HTMLDivElement>(null);
  const titleId = useId();

  // Escape closes, and Tab stays inside: a modal that leaks focus to the page
  // behind it is worse than no modal. The same rule the dialogs follow.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); onClose(); return; }
      if (e.key !== "Tab" || !box.current) return;
      const items = [...box.current.querySelectorAll<HTMLElement>("button, input, select, textarea, [href]")]
        .filter((el) => !el.hasAttribute("disabled"));
      if (items.length === 0) return;
      const firstEl = items[0], lastEl = items[items.length - 1];
      if (!e.shiftKey && document.activeElement === lastEl) { e.preventDefault(); firstEl.focus(); }
      if (e.shiftKey && document.activeElement === firstEl) { e.preventDefault(); lastEl.focus(); }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  // The first field, so a keyboard can start typing — and so the terminal
  // underneath stops being what keystrokes go to.
  useEffect(() => {
    const t = setTimeout(() => box.current?.querySelector<HTMLElement>("input, select, textarea")?.focus(), 0);
    return () => clearTimeout(t);
  }, []);

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 pt-[8vh]"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
    >
      <div
        ref={box}
        className={`w-full ${wide ? "max-w-3xl" : "max-w-xl"} overscroll-contain rounded-lg border border-border bg-surface shadow-float`}
      >
        <div className="flex items-start justify-between gap-3 border-b border-border px-4 py-3">
          <div className="min-w-0">
            <h2 id={titleId} className="truncate text-sm font-semibold">{title}</h2>
            {description && <p className="mt-0.5 text-xs text-ink-muted">{description}</p>}
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="-my-1 -mr-1 shrink-0 rounded-md px-2 py-1 text-ink-muted hover:bg-surface-2 hover:text-ink"
          >
            ✕
          </button>
        </div>
        <div className="p-4">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
