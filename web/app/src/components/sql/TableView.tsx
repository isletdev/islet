import { useCallback, useEffect, useMemo, useState } from "react";
import { run, sql, SqlError, type Cell, type Column, type ForeignKey, type TableDetail } from "@/lib/sql";
import { dialectOf } from "@/lib/sqlsplit";
import ResultsGrid, { quoteIdent, type FKLink } from "@/components/sql/ResultsGrid";
import { compact } from "@/components/sql/SchemaTree";
import { Button } from "@/components/ui";
import { CodeIcon, RefreshIcon } from "@/components/icons";

/**
 * One table's rows, with a filter, a sort and paging.
 *
 * The SQL it builds is on screen and can be taken into the editor. That is the
 * whole point of 7.7: a filter builder that hides its query teaches nothing,
 * and the audience here is someone who knows SQL or is learning it.
 */

export interface TableFilter {
  column: string;
  op: Op;
  value: string;
}

type Op = "=" | "<>" | ">" | ">=" | "<" | "<=" | "LIKE" | "IS NULL" | "IS NOT NULL";
const OPS: Op[] = ["=", "<>", ">", ">=", "<", "<=", "LIKE", "IS NULL", "IS NOT NULL"];
const PAGE_SIZES = [50, 100, 250, 500];

interface Props {
  connectionRef: string;
  engine: string;
  schema: string;
  table: string;
  /** A filter to open with, from following a foreign key. */
  initialFilter?: TableFilter | null;
  onOpenTable: (schema: string, table: string, filter?: TableFilter) => void;
  onOpenInEditor: (sql: string) => void;
}

export default function TableView({
  connectionRef, engine, schema, table, initialFilter, onOpenTable, onOpenInEditor,
}: Props) {
  const [detail, setDetail] = useState<TableDetail | null>(null);
  const [filters, setFilters] = useState<TableFilter[]>(initialFilter ? [initialFilter] : []);
  const [draft, setDraft] = useState<TableFilter>({ column: "", op: "=", value: "" });
  const [order, setOrder] = useState<{ column: string; dir: "ASC" | "DESC" } | null>(null);
  const [size, setSize] = useState(100);
  const [page, setPage] = useState(0);
  const [columns, setColumns] = useState<Column[]>([]);
  const [rows, setRows] = useState<Cell[][]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [more, setMore] = useState(false);
  const [showShape, setShowShape] = useState(false);

  const quote = useMemo(() => (engine === "mysql" || engine === "mariadb" ? backtick : quoteIdent), [engine]);
  const qualified = `${quote(schema)}.${quote(table)}`;

  const statement = useMemo(
    () => buildSelect(qualified, quote, filters, order, size, page),
    [qualified, quote, filters, order, size, page],
  );

  useEffect(() => {
    setFilters(initialFilter ? [initialFilter] : []);
    setPage(0);
    setOrder(null);
  }, [schema, table, initialFilter]);

  useEffect(() => {
    let alive = true;
    sql.table(connectionRef, schema, table)
      .then((d) => alive && setDetail(d))
      .catch(() => alive && setDetail(null));
    return () => { alive = false; };
  }, [connectionRef, schema, table]);

  const load = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      let got = false;
      await run(connectionRef, statement, { rowCap: size + 1 }, {
        onStatement: (r) => {
          got = true;
          if (r.error) { setError(r.error.message); return; }
          // One row over the page size is how the next-page button knows.
          const all = r.rows ?? [];
          setMore(all.length > size);
          setColumns(r.columns ?? []);
          setRows(all.slice(0, size));
        },
        onEnd: () => {},
        onError: (m) => setError(m),
      });
      if (!got) setError("the server returned nothing");
    } catch (e) {
      setError(e instanceof SqlError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [connectionRef, statement, size]);

  useEffect(() => { void load(); }, [load]);

  // A column that is the whole of a foreign key is a link to the row it names.
  const links = useMemo(() => {
    const out: Record<string, FKLink> = {};
    for (const fk of detail?.foreignKeys ?? []) {
      if (fk.columns.length !== 1) continue; // a composite key is not one click
      out[fk.columns[0]] = { fk, at: 0 };
    }
    return out;
  }, [detail]);

  const follow = (link: FKLink, value: Cell) => {
    onOpenTable(link.fk.refSchema, link.fk.refTable, {
      column: link.fk.refColumns[0],
      op: "=",
      value: String(value),
    });
  };

  const columnNames = (detail?.columns ?? []).map((c) => c.name);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <div className="min-w-0">
          <span className="text-sm font-medium">{schema}.{table}</span>
          {detail && (
            <span className="ml-2 text-[11px] text-ink-muted">
              {detail.kind}{detail.rows >= 0 ? ` · ~${compact(detail.rows)} rows` : ""}
            </span>
          )}
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="secondary" className="h-7 gap-1.5 px-2 text-xs" onClick={() => void load()}>
            <RefreshIcon className={`h-3.5 w-3.5 ${busy ? "animate-spin" : ""}`} />Refresh
          </Button>
          <Button variant="secondary" className="h-7 gap-1.5 px-2 text-xs" onClick={() => onOpenInEditor(statement)}>
            <CodeIcon className="h-3.5 w-3.5" />Open in the editor
          </Button>
          <Button
            variant="secondary"
            className="h-7 px-2 text-xs"
            onClick={() => setShowShape((s) => !s)}
          >
            {showShape ? "Hide the shape" : "Columns and keys"}
          </Button>
        </div>
      </div>

      {/* The filter builder, and the query it produced. */}
      <div className="border-b border-border px-3 py-2">
        <div className="flex flex-wrap items-center gap-1.5">
          {filters.map((f, i) => (
            <span key={i} className="flex items-center gap-1 rounded-md border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[11px]">
              {f.column} {f.op} {f.op.startsWith("IS") ? "" : f.value}
              <button
                type="button"
                onClick={() => setFilters((x) => x.filter((_, j) => j !== i))}
                className="ml-0.5 text-ink-faint hover:text-danger"
                aria-label="Remove this filter"
              >
                ×
              </button>
            </span>
          ))}
          <select
            value={draft.column}
            onChange={(e) => setDraft((d) => ({ ...d, column: e.target.value }))}
            className="h-7 rounded-md border border-border-strong bg-bg px-1.5 text-xs"
          >
            <option value="">Add a filter…</option>
            {columnNames.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
          {draft.column && (
            <>
              <select
                value={draft.op}
                onChange={(e) => setDraft((d) => ({ ...d, op: e.target.value as Op }))}
                className="h-7 rounded-md border border-border-strong bg-bg px-1.5 font-mono text-xs"
              >
                {OPS.map((o) => <option key={o} value={o}>{o}</option>)}
              </select>
              {!draft.op.startsWith("IS") && (
                <input
                  value={draft.value}
                  onChange={(e) => setDraft((d) => ({ ...d, value: e.target.value }))}
                  onKeyDown={(e) => { if (e.key === "Enter") addFilter(); }}
                  placeholder="value"
                  className="h-7 w-40 rounded-md border border-border-strong bg-bg px-2 font-mono text-xs"
                />
              )}
              <Button className="h-7 px-2 text-xs" onClick={addFilter}>Add</Button>
            </>
          )}

          <div className="ml-auto flex items-center gap-1.5 text-xs">
            <select
              value={order ? order.column + " " + order.dir : ""}
              onChange={(e) => {
                const v = e.target.value;
                if (!v) { setOrder(null); return; }
                const [column, dir] = v.split(" ");
                setOrder({ column, dir: dir as "ASC" | "DESC" });
                setPage(0);
              }}
              className="h-7 rounded-md border border-border-strong bg-bg px-1.5 text-xs"
            >
              <option value="">No sort</option>
              {columnNames.flatMap((n) => [
                <option key={n + "a"} value={n + " ASC"}>{n} ↑</option>,
                <option key={n + "d"} value={n + " DESC"}>{n} ↓</option>,
              ])}
            </select>
            <select
              value={size}
              onChange={(e) => { setSize(Number(e.target.value)); setPage(0); }}
              className="h-7 rounded-md border border-border-strong bg-bg px-1.5 text-xs"
            >
              {PAGE_SIZES.map((n) => <option key={n} value={n}>{n} rows</option>)}
            </select>
          </div>
        </div>

        <pre className="mt-2 overflow-x-auto rounded-md border border-border bg-surface-2 px-2 py-1.5 font-mono text-[11px] text-ink">{statement}</pre>
      </div>

      {showShape && detail && <Shape detail={detail} onOpenTable={onOpenTable} />}

      {error && <p className="border-b border-border bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>}

      <ResultsGrid
        columns={columns}
        rows={rows}
        links={links}
        onFollow={follow}
        widthKey={`${connectionRef}:${schema}.${table}`}
        empty={filters.length ? "No rows match this filter." : "This table is empty."}
        className="flex-1"
      />

      <div className="flex items-center gap-2 border-t border-border px-3 py-1.5 text-xs text-ink-muted">
        <span>Rows {page * size + 1}–{page * size + rows.length}</span>
        <div className="ml-auto flex items-center gap-1.5">
          <Button
            variant="secondary"
            className="h-7 px-2 text-xs"
            disabled={page === 0 || busy}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
          >
            Previous
          </Button>
          <Button variant="secondary" className="h-7 px-2 text-xs" disabled={!more || busy} onClick={() => setPage((p) => p + 1)}>
            Next
          </Button>
        </div>
      </div>
    </div>
  );

  function addFilter() {
    if (!draft.column) return;
    setFilters((f) => [...f, draft]);
    setDraft({ column: "", op: "=", value: "" });
    setPage(0);
  }
}

/** Columns, keys, indexes and what points here. */
function Shape({ detail, onOpenTable }: { detail: TableDetail; onOpenTable: (s: string, t: string) => void }) {
  return (
    <div className="grid grid-cols-1 gap-4 border-b border-border px-3 py-3 text-xs lg:grid-cols-3">
      <div>
        <h4 className="mb-1.5 font-medium">Columns</h4>
        <table className="w-full">
          <tbody>
            {(detail.columns ?? []).map((c) => (
              <tr key={c.name} className="align-baseline">
                <td className="py-0.5 pr-2">
                  {c.primaryKey && <span className="mr-1 text-[9px] text-accent">PK</span>}
                  {c.name}
                </td>
                <td className="py-0.5 pr-2 font-mono text-[11px] text-ink-muted">{c.type}</td>
                <td className="py-0.5 text-[11px] text-ink-faint">
                  {c.nullable ? "" : "not null"}{c.identity ? " identity" : ""}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div>
        <h4 className="mb-1.5 font-medium">Indexes</h4>
        {detail.indexes.length === 0 && <p className="text-ink-faint">None.</p>}
        {detail.indexes.map((i) => (
          <p key={i.name} className="py-0.5">
            <span className="font-mono text-[11px]">{i.name}</span>
            <span className="ml-1.5 text-ink-muted">({i.columns.join(", ")})</span>
            {i.unique && <span className="ml-1.5 text-[10px] text-accent">unique</span>}
          </p>
        ))}
        {detail.constraints.length > 0 && (
          <>
            <h4 className="mb-1.5 mt-3 font-medium">Constraints</h4>
            {detail.constraints.map((c) => (
              <p key={c.name} className="py-0.5">
                <span className="font-mono text-[11px]">{c.name}</span>
                <span className="ml-1.5 text-ink-muted">{c.definition}</span>
              </p>
            ))}
          </>
        )}
      </div>
      <div>
        <h4 className="mb-1.5 font-medium">Relationships</h4>
        <Relations title="Points at" keys={detail.foreignKeys ?? []} pick={(fk) => [fk.refSchema, fk.refTable]} onOpen={onOpenTable} />
        <Relations title="Pointed at by" keys={detail.referencedBy ?? []} pick={(fk) => [fk.schema, fk.table]} onOpen={onOpenTable} />
      </div>
    </div>
  );
}

function Relations({
  title, keys, pick, onOpen,
}: {
  title: string;
  keys: ForeignKey[];
  pick: (fk: ForeignKey) => [string, string];
  onOpen: (s: string, t: string) => void;
}) {
  if (keys.length === 0) return <p className="py-0.5 text-ink-faint">{title}: nothing.</p>;
  return (
    <div className="py-0.5">
      <p className="text-ink-muted">{title}:</p>
      {keys.map((fk) => {
        const [s, t] = pick(fk);
        return (
          <button
            key={fk.name}
            type="button"
            onClick={() => onOpen(s, t)}
            className="block py-0.5 text-left text-accent hover:underline"
          >
            {s}.{t} <span className="text-ink-faint">({fk.columns.join(", ")})</span>
          </button>
        );
      })}
    </div>
  );
}

function backtick(name: string) {
  return "`" + name.replaceAll("`", "``") + "`";
}

/**
 * Build the SELECT the viewer runs.
 *
 * Values are single-quoted with the quote doubled. That is escaping rather than
 * binding, which is the thing 8.7 says not to do — so it is bounded to what the
 * filter builder produces, the statement is on screen before it runs, and the
 * whole thing goes through the same classifier and read-only checks as anything
 * typed by hand.
 */
export function buildSelect(
  qualified: string,
  quote: (s: string) => string,
  filters: TableFilter[],
  order: { column: string; dir: "ASC" | "DESC" } | null,
  size: number,
  page: number,
): string {
  let out = `SELECT *\nFROM ${qualified}`;
  if (filters.length) {
    out += "\nWHERE " + filters.map((f) => {
      if (f.op === "IS NULL" || f.op === "IS NOT NULL") return `${quote(f.column)} ${f.op}`;
      return `${quote(f.column)} ${f.op} '${f.value.replaceAll("'", "''")}'`;
    }).join("\n  AND ");
  }
  if (order) out += `\nORDER BY ${quote(order.column)} ${order.dir}`;
  out += `\nLIMIT ${size + 1}`;
  if (page > 0) out += ` OFFSET ${page * size}`;
  return out;
}

/** The dialect decides how an identifier is quoted; exported for the page. */
export function quoterFor(engine: string) {
  return dialectOf(engine) === "mysql" ? backtick : quoteIdent;
}
