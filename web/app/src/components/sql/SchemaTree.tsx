import { useMemo, useState } from "react";
import type { Table, Tree } from "@/lib/sql";
import { DatabasesIcon, RefreshIcon, SearchIcon } from "@/components/icons";

/**
 * The schema, as a tree.
 *
 * Everything here comes from one cached introspection pass, so expanding a
 * table is not a round trip and the search is instant. Row counts are the
 * catalog's estimate: counting a table to draw a tree is how a schema browser
 * becomes the slowest page in a panel.
 */

interface Props {
  tree: Tree | null;
  loading: boolean;
  error?: string | null;
  onRefresh: () => void;
  onOpenTable: (t: Table) => void;
  onInsert: (text: string) => void;
  /** The table currently open, so the tree can say where you are. */
  active?: { schema: string; name: string } | null;
}

export default function SchemaTree({ tree, loading, error, onRefresh, onOpenTable, onInsert, active }: Props) {
  const [filter, setFilter] = useState("");
  const [openSchemas, setOpenSchemas] = useState<Record<string, boolean>>({});
  const [openTables, setOpenTables] = useState<Record<string, boolean>>({});

  const schemas = useMemo(() => {
    if (!tree) return [];
    const q = filter.trim().toLowerCase();
    if (!q) return tree.schemas;
    return tree.schemas
      .map((s) => ({
        ...s,
        tables: s.tables.filter(
          (t) => t.name.toLowerCase().includes(q) ||
            (t.columns ?? []).some((c) => c.name.toLowerCase().includes(q)),
        ),
      }))
      .filter((s) => s.tables.length > 0 || s.name.toLowerCase().includes(q));
  }, [tree, filter]);

  // One schema means no reason to make anyone click it open.
  const isOpen = (name: string) =>
    openSchemas[name] ?? (filter.trim() !== "" || (tree?.schemas.length ?? 0) <= 2);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-1.5 border-b border-border px-2 py-1.5">
        <div className="relative min-w-0 flex-1">
          <SearchIcon className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-faint" />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter tables and columns"
            className="h-7 w-full rounded-md border border-border bg-bg pl-7 pr-2 text-xs text-ink placeholder:text-ink-faint focus:border-accent"
          />
        </div>
        <button
          type="button"
          onClick={onRefresh}
          title="Read the schema again"
          className="shrink-0 rounded-md p-1.5 text-ink-muted hover:bg-surface-2 hover:text-ink"
        >
          <RefreshIcon className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
        </button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-1 py-1.5">
        {error && <p className="px-2 py-3 text-xs text-danger">{error}</p>}
        {!error && !tree && loading && <p className="px-2 py-3 text-xs text-ink-muted">Reading the schema…</p>}
        {!error && tree && schemas.length === 0 && (
          <p className="px-2 py-3 text-xs text-ink-muted">{filter ? "Nothing matches." : "This database has no tables yet."}</p>
        )}

        {schemas.map((s) => (
          <div key={s.name}>
            <button
              type="button"
              onClick={() => setOpenSchemas((o) => ({ ...o, [s.name]: !isOpen(s.name) }))}
              className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-xs font-medium hover:bg-surface-2"
            >
              <Caret open={isOpen(s.name)} />
              <DatabasesIcon className="h-3.5 w-3.5 shrink-0 text-ink-faint" />
              <span className="truncate">{s.name}</span>
              <span className="ml-auto shrink-0 text-[10px] text-ink-faint">{s.tables.length}</span>
            </button>

            {isOpen(s.name) && s.tables.map((t) => {
              const key = s.name + "." + t.name;
              const on = openTables[key] ?? false;
              const here = active?.schema === t.schema && active?.name === t.name;
              return (
                <div key={key} className="ml-3">
                  <div className={`group flex items-center gap-1 rounded-md pr-1 ${here ? "bg-surface-2" : "hover:bg-surface-2/70"}`}>
                    <button
                      type="button"
                      onClick={() => setOpenTables((o) => ({ ...o, [key]: !on }))}
                      className="shrink-0 px-1 py-1 text-ink-faint hover:text-ink"
                      aria-label={on ? "Hide columns" : "Show columns"}
                    >
                      <Caret open={on} />
                    </button>
                    <button
                      type="button"
                      onClick={() => onOpenTable(t)}
                      className="flex min-w-0 flex-1 items-baseline gap-1.5 py-1 text-left text-xs"
                      title={t.comment || `${t.kind} ${t.schema}.${t.name}`}
                    >
                      <span className={`truncate ${t.kind === "table" ? "" : "italic text-ink-muted"}`}>{t.name}</span>
                      {t.kind !== "table" && <span className="shrink-0 text-[10px] text-ink-faint">{t.kind}</span>}
                      {t.rows >= 0 && <span className="ml-auto shrink-0 font-mono text-[10px] tabular-nums text-ink-faint">{compact(t.rows)}</span>}
                    </button>
                  </div>

                  {on && (
                    <div className="ml-5 border-l border-border pl-2">
                      {(t.columns ?? []).map((c) => (
                        <button
                          key={c.name}
                          type="button"
                          onClick={() => onInsert(c.name)}
                          title={`${c.type}${c.nullable ? "" : " NOT NULL"}${c.default ? " default " + c.default : ""}${c.comment ? " — " + c.comment : ""}`}
                          className="flex w-full items-baseline gap-1.5 rounded-md px-1.5 py-0.5 text-left text-[11px] hover:bg-surface-2"
                        >
                          {c.primaryKey && <span className="shrink-0 text-[9px] text-accent" title="Primary key">PK</span>}
                          <span className="truncate">{c.name}</span>
                          <span className="ml-auto shrink-0 truncate font-mono text-[10px] text-ink-faint">{c.type}</span>
                        </button>
                      ))}
                      {(t.foreignKeys ?? []).map((fk) => (
                        <div key={"fk" + fk.name} className="px-1.5 py-0.5 text-[10px] text-ink-faint">
                          {fk.columns.join(", ")} → {fk.refSchema}.{fk.refTable}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        ))}

        {tree?.partial && (
          <p className="mt-2 px-2 text-[11px] text-warning">
            This schema is larger than the tree can hold; some objects are not listed.
          </p>
        )}
      </div>
    </div>
  );
}

function Caret({ open }: { open: boolean }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={`h-3 w-3 shrink-0 transition-transform ${open ? "rotate-90" : ""}`}>
      <path d="M9 6l6 6-6 6" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** A row estimate, short enough to sit at the end of a line. */
export function compact(n: number): string {
  if (n < 0) return "";
  if (n < 1000) return String(n);
  if (n < 1_000_000) return (n / 1000).toFixed(n < 10_000 ? 1 : 0) + "k";
  if (n < 1_000_000_000) return (n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0) + "M";
  return (n / 1_000_000_000).toFixed(1) + "B";
}
