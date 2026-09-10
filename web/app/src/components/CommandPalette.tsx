import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { NAV } from "@/nav";

export interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

/** Cmd/Ctrl+K palette. Navigation plus a few actions; features register more later. */
export default function CommandPalette({ extra = [] }: { extra?: Command[] }) {
  const nav = useNavigate();
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [idx, setIdx] = useState(0);
  const input = useRef<HTMLInputElement>(null);

  const commands = useMemo<Command[]>(() => [
    ...NAV.map((n) => ({ id: `go:${n.path}`, label: `Go to ${n.label}`, hint: n.phase !== "v0.1" ? `planned ${n.phase}` : undefined, run: () => nav(n.path) })),
    ...extra,
  ], [nav, extra]);

  const results = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return commands;
    return commands.filter((c) => {
      const l = c.label.toLowerCase();
      let i = 0;
      for (const ch of s) { i = l.indexOf(ch, i); if (i < 0) return false; i++; }
      return true;
    });
  }, [q, commands]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setOpen((o) => !o); setQ(""); setIdx(0); }
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => { if (open) setTimeout(() => input.current?.focus(), 0); }, [open]);
  useEffect(() => { setIdx(0); }, [q]);

  if (!open) return null;

  const pick = (c: Command) => { setOpen(false); c.run(); };

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-[12vh]" onClick={() => setOpen(false)} role="dialog" aria-modal="true" aria-label="Command palette">
      <div className="w-full max-w-lg overflow-hidden rounded-lg border border-border bg-surface shadow-float" onClick={(e) => e.stopPropagation()}>
        <input
          ref={input}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") { e.preventDefault(); setIdx((i) => Math.min(results.length - 1, i + 1)); }
            if (e.key === "ArrowUp") { e.preventDefault(); setIdx((i) => Math.max(0, i - 1)); }
            if (e.key === "Enter" && results[idx]) pick(results[idx]);
          }}
          placeholder="Type a command or a page…"
          className="h-11 w-full border-b border-border bg-transparent px-4 text-sm outline-none placeholder:text-ink-faint"
          aria-label="Search commands"
        />
        <ul className="max-h-80 overflow-y-auto py-1" role="listbox">
          {results.map((c, i) => (
            <li
              key={c.id}
              role="option"
              aria-selected={i === idx}
              onMouseEnter={() => setIdx(i)}
              onClick={() => pick(c)}
              className={`flex cursor-pointer items-center justify-between px-4 py-2 text-sm ${i === idx ? "bg-surface-2 text-ink" : "text-ink-muted"}`}
            >
              <span>{c.label}</span>
              {c.hint && <span className="font-mono text-[11px] text-ink-faint">{c.hint}</span>}
            </li>
          ))}
          {results.length === 0 && <li className="px-4 py-3 text-sm text-ink-muted">Nothing matches.</li>}
        </ul>
        <div className="flex items-center gap-3 border-t border-border px-4 py-1.5 font-mono text-[11px] text-ink-faint">
          <span>↑↓ move</span><span>↵ run</span><span>esc close</span>
        </div>
      </div>
    </div>
  );
}
