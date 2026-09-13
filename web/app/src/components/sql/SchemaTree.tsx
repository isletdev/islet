import { useMemo, useState, type ReactNode } from "react";
import type { ColumnInfo, Table, Tree } from "@/lib/sql";
import { DatabasesIcon, RefreshIcon, SearchIcon } from "@/components/icons";

/**
 * The schema, as a tree.
 *
 * Everything here comes from one cached introspection pass, so expanding a
 * table is not a round trip and the filter is instant. Row counts are the
 * catalog's estimate: counting a table to draw a tree is how a schema browser
 * becomes the slowest page in a panel.
 *
 * Filtering searches columns as well as tables, so a table can be in the list
 * for a reason that is not visible on its own row. When that happens it opens
 * itself and shows the columns that matched, because a result you have to go
 * looking for is not a result.
 */

interface Props {
  tree: Tree | null;
  loading: boolean;
  error?: string | null;
  onRefresh: () => void;
  onOpenTable: (t: Table) => void;
  /** The table currently open, so the tree can say where you are. */
  active?: { schema: string; name: string } | null;
}

interface Hit {
  table: Table;
  /** The table's own name matched. */
  byName: boolean;
  /** The columns that matched, empty when the filter is empty. */
  columns: ColumnInfo[];
}

export default function SchemaTree({ tree, loading, error, onRefresh, onOpenTable, active }: Props) {
  const [filter, setFilter] = useState("");
  const [openSchemas, setOpenSchemas] = useState<Record<string, boolean>>({});
  const [openTables, setOpenTables] = useState<Record<string, boolean>>({});

  const q = filter.trim().toLowerCase();

  const schemas = useMemo(() => {
    if (!tree) return [] as { name: string; hits: Hit[] }[];
    return tree.schemas
      .map((s) => ({
        name: s.name,
        hits: s.tables
          .map((table): Hit => {
            if (!q) return { table, byName: false, columns: [] };
            const byName = table.name.toLowerCase().includes(q);
            const columns = (table.columns ?? []).filter((c) => c.name.toLowerCase().includes(q));
            return { table, byName, columns };
          })
          .filter((h) => !q || h.byName || h.columns.length > 0),
      }))
      .filter((s) => !q || s.hits.length > 0 || s.name.toLowerCase().includes(q));
  }, [tree, q]);

  // One schema means no reason to make anyone click it open.
  const schemaOpen = (name: string) =>
    openSchemas[name] ?? (q !== "" || (tree?.schemas.length ?? 0) <= 2);

  // A table matched by one of its columns opens itself: otherwise it sits
  // there collapsed, apparently for no reason, and the person has to click it
  // to find out why their search found it.
  const tableOpen = (key: string, hit: Hit) =>
    openTables[key] ?? (q !== "" && hit.columns.length > 0);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-1.5 border-b border-border px-2 py-1.5">
        <div className="relative min-w-0 flex-1">
          <SearchIcon className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-faint" />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter tables and columns"
            className="h-7 w-full rounded-md border border-border bg-bg pl-7 pr-7 text-xs text-ink placeholder:text-ink-faint focus:border-accent"
          />
          {filter && (
            <button
              type="button"
              onClick={() => setFilter("")}
              aria-label="Clear the filter"
              className="absolute right-1 top-1/2 -translate-y-1/2 rounded-sm p-1 text-ink-faint hover:text-ink"
            >
              <svg viewBox="0 0 24 24" className="h-3 w-3" aria-hidden="true">
                <path d="M6 6l12 12M18 6L6 18" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" />
              </svg>
            </button>
          )}
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
          <p className="px-2 py-3 text-xs text-ink-muted">{q ? "Nothing matches." : "This database has no tables yet."}</p>
        )}

        {schemas.map((s) => (
          <div key={s.name}>
            <button
              type="button"
              onClick={() => setOpenSchemas((o) => ({ ...o, [s.name]: !schemaOpen(s.name) }))}
              aria-expanded={schemaOpen(s.name)}
              className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-xs font-medium hover:bg-surface-2"
            >
              <Caret open={schemaOpen(s.name)} />
              <DatabasesIcon className="h-3.5 w-3.5 shrink-0 text-ink-faint" />
              <span className="truncate">{s.name}</span>
              <span className="ml-auto shrink-0 text-[10px] text-ink-faint">{s.hits.length}</span>
            </button>

            {schemaOpen(s.name) && s.hits.map((hit) => {
              const t = hit.table;
              const key = s.name + "." + t.name;
              const on = tableOpen(key, hit);
              const here = active?.schema === t.schema && active?.name === t.name;
              // While filtering, the columns shown are the ones that matched.
              const columns = q && hit.columns.length > 0 && !hit.byName ? hit.columns : (t.columns ?? []);
              return (
                <div key={key} className="ml-3">
                  {/* Clicking the table opens it and shows its columns. The
                      chevron on its own only opens the columns, for looking at
                      the shape without loading rows. */}
                  <button
                    type="button"
                    onClick={() => { setOpenTables((o) => ({ ...o, [key]: true })); onOpenTable(t); }}
                    className={`flex w-full items-center rounded-md py-1 pl-1 pr-2 text-left text-xs ${here ? "bg-surface-2" : "hover:bg-surface-2/70"}`}
                    title={t.comment || `Open ${t.schema}.${t.name}`}
                  >
                    <span
                      role="button"
                      tabIndex={-1}
                      aria-expanded={on}
                      aria-label={on ? "Hide the columns" : "Show the columns"}
                      onClick={(e) => { e.stopPropagation(); setOpenTables((o) => ({ ...o, [key]: !on })); }}
                      className="-my-1 shrink-0 rounded-sm px-0.5 py-1 text-ink-faint hover:text-ink"
                    >
                      <Caret open={on} />
                    </span>
                    <span className={`ml-1 truncate ${t.kind === "table" ? "" : "italic text-ink-muted"}`}>
                      <Mark text={t.name} match={q} />
                    </span>
                    {t.kind !== "table" && <span className="ml-1.5 shrink-0 text-[10px] text-ink-faint">{t.kind}</span>}
                    {q && !hit.byName && hit.columns.length > 0 && (
                      <span className="ml-1.5 shrink-0 text-[10px] text-accent">
                        {hit.columns.length} column{hit.columns.length === 1 ? "" : "s"}
                      </span>
                    )}
                    {t.rows >= 0 && <span className="ml-auto shrink-0 pl-1.5 font-mono text-[10px] tabular-nums text-ink-faint">{compact(t.rows)}</span>}
                  </button>

                  {on && (
                    <div className="ml-5 border-l border-border pl-2">
                      {columns.map((c) => (
                        <div
                          key={c.name}
                          title={`${c.type}${c.nullable ? "" : " NOT NULL"}${c.default ? " default " + c.default : ""}${c.comment ? " — " + c.comment : ""}`}
                          className="flex w-full items-baseline gap-1.5 px-1.5 py-0.5 text-[11px]"
                        >
                          {c.primaryKey && <span className="shrink-0 text-[9px] text-accent" title="Primary key">PK</span>}
                          <span className="truncate"><Mark text={c.name} match={q} /></span>
                          <span className="ml-auto shrink-0 truncate font-mono text-[10px] text-ink-faint">{c.type}</span>
                        </div>
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

/** The part of a name that matched, so a long list says why each row is in it. */
function Mark({ text, match }: { text: string; match: string }): ReactNode {
  if (!match) return text;
  const at = text.toLowerCase().indexOf(match);
  if (at < 0) return text;
  return (
    <>
      {text.slice(0, at)}
      <span className="rounded-[2px] bg-accent-soft text-accent">{text.slice(at, at + match.length)}</span>
      {text.slice(at + match.length)}
    </>
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
