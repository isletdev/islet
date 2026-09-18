import { useEffect, useMemo, useRef, useState } from "react";
import { COUNTRY_CODES, countryName } from "@/lib/countries";
import { CloseIcon, SearchIcon } from "@/components/icons";

/**
 * Pick any number of countries out of all 249 of them.
 *
 * What this replaces was nine checkboxes — China, Russia, North Korea and six
 * others — beside a text box for "more codes, comma separated". That is a list
 * of somebody's assumptions about who you are, and everyone else had to know
 * the two-letter code for the country they meant and type it without a spell
 * check. A server that only ever serves one country needs to say so by naming
 * the other two hundred, which no row of checkboxes can do.
 *
 * Search matches the name or the code, so both "germany" and "de" find the same
 * row, and the names come from the browser in the reader's own language.
 */
export default function CountryPicker({ value, onChange }: { value: string[]; onChange: (next: string[]) => void }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const box = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);

  // Built once: 249 Intl lookups per keystroke is work nobody asked for.
  const all = useMemo(() => COUNTRY_CODES.map((cc) => ({ cc, name: countryName(cc) })).sort((a, b) => a.name.localeCompare(b.name)), []);
  // Ranked, not just filtered. Typing "de" matched Bangladesh, Cape Verde and
  // Denmark before it matched Germany, whose code is exactly that — so the
  // fastest way to pick a country you know the code for was the slowest.
  const matches = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return all;
    const rank = (c: { cc: string; name: string }) => {
      const name = c.name.toLowerCase();
      if (c.cc === s) return 0;               // the code, exactly
      if (name === s) return 1;               // the name, exactly
      if (name.startsWith(s)) return 2;       // the name, from the start
      if (name.includes(s)) return 3;         // the name, somewhere
      if (c.cc.startsWith(s)) return 4;       // the code, from the start
      return 5;
    };
    return all
      .map((c) => ({ c, r: rank(c) }))
      .filter((x) => x.r < 5)
      .sort((a, b) => a.r - b.r || a.c.name.localeCompare(b.c.name))
      .map((x) => x.c);
  }, [all, q]);

  // Clicking away closes it, which is what a dropdown is expected to do and the
  // only way out on a touch screen with no Escape key.
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [open]);
  useEffect(() => { if (open) input.current?.focus(); }, [open]);

  const toggle = (cc: string) => onChange(value.includes(cc) ? value.filter((x) => x !== cc) : [...value, cc]);

  return (
    <div ref={box} className="relative">
      {/* What is chosen, always visible and always removable — a count alone
          would mean opening the list to find out what is in it. */}
      <div className="flex flex-wrap items-center gap-1.5">
        {value.map((cc) => (
          <span key={cc} className="inline-flex items-center gap-1 rounded-md bg-surface-2 py-1 pl-2 pr-1 text-xs">
            {countryName(cc)}
            <button
              type="button"
              onClick={() => toggle(cc)}
              aria-label={`Stop blocking ${countryName(cc)}`}
              className="rounded-sm p-0.5 text-ink-muted hover:text-danger"
            >
              <CloseIcon className="h-3 w-3" />
            </button>
          </span>
        ))}
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          className="-my-1 inline-flex items-center gap-1 rounded-md border border-border-strong px-2 py-1 text-xs text-ink-muted hover:bg-surface-2 hover:text-ink"
        >
          <SearchIcon className="h-3.5 w-3.5" />
          {value.length === 0 ? "Choose countries" : "Add another"}
        </button>
        {value.length > 0 && (
          <button type="button" onClick={() => onChange([])} className="-my-1 py-1 text-xs text-ink-muted hover:text-ink">
            Clear {value.length}
          </button>
        )}
      </div>

      {open && (
        <div className="absolute z-20 mt-1.5 w-full max-w-sm rounded-md border border-border-strong bg-surface shadow-lg">
          <div className="border-b border-border p-2">
            <input
              ref={input}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") { setOpen(false); return; }
                // Enter takes the first match, so a country can be added
                // without the hand leaving the keyboard.
                if (e.key === "Enter" && matches[0]) { e.preventDefault(); toggle(matches[0].cc); setQ(""); }
              }}
              placeholder="Search 249 countries, by name or code"
              aria-label="Search countries"
              className="w-full rounded-sm border border-border bg-bg px-2 py-1.5 text-xs"
            />
          </div>
          <ul className="max-h-64 overflow-y-auto p-1" role="listbox" aria-multiselectable>
            {matches.map((c) => (
              <li key={c.cc}>
                <label className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-xs hover:bg-surface-2">
                  <input type="checkbox" checked={value.includes(c.cc)} onChange={() => toggle(c.cc)} />
                  <span className="flex-1 truncate">{c.name}</span>
                  <span className="font-mono text-[10px] text-ink-faint uppercase">{c.cc}</span>
                </label>
              </li>
            ))}
            {matches.length === 0 && <li className="px-2 py-3 text-center text-xs text-ink-muted">Nothing matches “{q}”.</li>}
          </ul>
        </div>
      )}
    </div>
  );
}
