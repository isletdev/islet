import { useEffect, useMemo, useRef, useState } from "react";
import type { Cell, Class, Column, ForeignKey } from "@/lib/sql";

/**
 * A result set.
 *
 * Rendering is by class, not by guessing at the value: NULL is visibly not an
 * empty string, a bigint stays exact because it arrived as a string, and JSON
 * is an object you can open rather than a line of braces. The row cap means a
 * page is at most 50,000 rows, so the rows are windowed rather than all put in
 * the DOM at once; column widths are remembered per result shape.
 */

const ROW_HEIGHT = 28;
const OVERSCAN = 12;

export interface FKLink {
  /** The key this column belongs to, and the column's place in it. */
  fk: ForeignKey;
  at: number;
}

interface Props {
  columns: Column[];
  rows: Cell[][];
  /** Keyed by column name: following one opens the referenced row. */
  links?: Record<string, FKLink>;
  onFollow?: (link: FKLink, value: Cell) => void;
  /** Remembering widths needs a stable name for this shape of result. */
  widthKey?: string;
  empty?: string;
  className?: string;
}

export default function ResultsGrid({ columns, rows, links, onFollow, widthKey, empty, className = "" }: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const [top, setTop] = useState(0);
  const [height, setHeight] = useState(400);
  const [sort, setSort] = useState<{ col: number; dir: 1 | -1 } | null>(null);
  const [widths, setWidths] = useState<Record<string, number>>(() => loadWidths(widthKey));
  const [open, setOpen] = useState<{ row: number; col: number } | null>(null);

  useEffect(() => { setSort(null); setTop(0); scroller.current?.scrollTo(0, 0); }, [columns, rows]);
  useEffect(() => { setWidths(loadWidths(widthKey)); }, [widthKey]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setHeight(el.clientHeight));
    ro.observe(el);
    setHeight(el.clientHeight);
    return () => ro.disconnect();
  }, []);

  // Sorting is local to the rows already fetched, which is the honest thing to
  // offer: the caller says so when the result was truncated.
  const order = useMemo(() => {
    const idx = rows.map((_, i) => i);
    if (!sort) return idx;
    const cls = columns[sort.col]?.class ?? "unknown";
    idx.sort((a, b) => compare(rows[a][sort.col], rows[b][sort.col], cls) * sort.dir);
    return idx;
  }, [rows, sort, columns]);

  const first = Math.max(0, Math.floor(top / ROW_HEIGHT) - OVERSCAN);
  const last = Math.min(order.length, Math.ceil((top + height) / ROW_HEIGHT) + OVERSCAN);

  const setWidth = (name: string, w: number) => {
    const next = { ...widths, [name]: Math.max(60, Math.min(800, w)) };
    setWidths(next);
    saveWidths(widthKey, next);
  };

  if (columns.length === 0) {
    return <div className={`p-6 text-center text-sm text-ink-muted ${className}`}>{empty ?? "No columns."}</div>;
  }

  return (
    <div
      ref={scroller}
      onScroll={(e) => setTop(e.currentTarget.scrollTop)}
      className={`min-h-0 overflow-auto ${className}`}
    >
      <table className="w-max min-w-full border-separate border-spacing-0 text-[12px]">
        <thead className="sticky top-0 z-10">
          <tr>
            <th className="sticky left-0 z-20 w-10 border-b border-r border-border bg-surface-2 px-2 py-1.5 text-right font-normal text-ink-faint">
              #
            </th>
            {columns.map((c, i) => (
              <th
                key={c.name + i}
                style={{ width: widths[c.name] ?? undefined, minWidth: widths[c.name] ?? 90 }}
                className="group relative border-b border-r border-border bg-surface-2 px-2 py-1.5 text-left font-medium"
              >
                <button
                  type="button"
                  onClick={() => setSort((s) => (s && s.col === i ? (s.dir === 1 ? { col: i, dir: -1 } : null) : { col: i, dir: 1 }))}
                  className="flex max-w-full items-baseline gap-1.5 text-left hover:text-accent"
                  title={`${c.name} — ${c.type}`}
                >
                  <span className="truncate">{c.name}</span>
                  <span className="shrink-0 font-mono text-[10px] font-normal text-ink-faint">{c.type}</span>
                  {sort?.col === i && <span className="shrink-0 text-accent">{sort.dir === 1 ? "↑" : "↓"}</span>}
                </button>
                <Grip onDrag={(dx, startW) => setWidth(c.name, startW + dx)} width={widths[c.name] ?? 0} />
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {first > 0 && <tr style={{ height: first * ROW_HEIGHT }}><td colSpan={columns.length + 1} /></tr>}
          {order.slice(first, last).map((r, n) => (
            <tr key={r} className="even:bg-surface-2/30 hover:bg-surface-2/70">
              <td className="sticky left-0 z-10 border-b border-r border-border bg-surface px-2 text-right font-mono text-[11px] text-ink-faint">
                {first + n + 1}
              </td>
              {columns.map((c, i) => {
                const value = rows[r][i];
                const link = links?.[c.name];
                return (
                  <td
                    key={c.name + i}
                    onDoubleClick={() => setOpen({ row: r, col: i })}
                    style={{ maxWidth: widths[c.name] ?? 420 }}
                    className="h-7 overflow-hidden whitespace-nowrap border-b border-r border-border px-2 align-middle"
                  >
                    <CellView
                      value={value}
                      cls={c.class}
                      link={link && value !== null ? () => onFollow?.(link, value) : undefined}
                      onExpand={() => setOpen({ row: r, col: i })}
                    />
                  </td>
                );
              })}
            </tr>
          ))}
          {last < order.length && (
            <tr style={{ height: (order.length - last) * ROW_HEIGHT }}><td colSpan={columns.length + 1} /></tr>
          )}
          {order.length === 0 && (
            <tr>
              <td colSpan={columns.length + 1} className="px-3 py-6 text-center text-ink-muted">
                {empty ?? "No rows."}
              </td>
            </tr>
          )}
        </tbody>
      </table>

      {open && (
        <CellSheet
          column={columns[open.col]}
          value={rows[open.row]?.[open.col]}
          onClose={() => setOpen(null)}
        />
      )}
    </div>
  );
}

function Grip({ onDrag, width }: { onDrag: (dx: number, startWidth: number) => void; width: number }) {
  return (
    <span
      role="separator"
      aria-orientation="vertical"
      onMouseDown={(e) => {
        e.preventDefault();
        const startX = e.clientX;
        const th = (e.currentTarget.parentElement as HTMLElement);
        const startW = width || th.getBoundingClientRect().width;
        const move = (ev: MouseEvent) => onDrag(ev.clientX - startX, startW);
        const up = () => { document.removeEventListener("mousemove", move); document.removeEventListener("mouseup", up); };
        document.addEventListener("mousemove", move);
        document.addEventListener("mouseup", up);
      }}
      className="absolute inset-y-0 right-0 w-1.5 cursor-col-resize bg-transparent hover:bg-accent/40"
    />
  );
}

function CellView({ value, cls, link, onExpand }: { value: Cell; cls: Class; link?: () => void; onExpand: () => void }) {
  if (value === null || value === undefined) {
    return <span className="rounded-sm bg-surface-2 px-1 font-mono text-[10px] uppercase tracking-wide text-ink-faint">null</span>;
  }
  if (cls === "bool") {
    return <span className={value ? "text-success" : "text-ink-muted"}>{String(value)}</span>;
  }
  if (cls === "json" || cls === "array" || (value !== null && typeof value === "object")) {
    const text = JSON.stringify(value);
    return (
      <button type="button" onClick={onExpand} className="truncate font-mono text-[11px] text-accent hover:underline" title="Open">
        {text.length > 90 ? text.slice(0, 90) + "…" : text}
      </button>
    );
  }
  if (cls === "binary") {
    const s = String(value);
    return (
      <button type="button" onClick={onExpand} className="font-mono text-[11px] text-ink-muted hover:text-ink" title="Open">
        {bytesLabel(s)}
      </button>
    );
  }
  const text = String(value);
  const numeric = cls === "number" || cls === "decimal";
  const body = (
    <span className={`block truncate ${numeric ? "text-right font-mono tabular-nums" : ""} ${cls === "uuid" || cls === "datetime" || cls === "date" || cls === "time" ? "font-mono text-[11px]" : ""}`}>
      {text === "" ? <span className="text-ink-faint">(empty)</span> : text}
    </span>
  );
  if (link) {
    return (
      <button type="button" onClick={link} className="block w-full truncate text-left text-accent hover:underline" title="Open the referenced row">
        {text}
      </button>
    );
  }
  if (text.length > 120) {
    return (
      <button type="button" onClick={onExpand} className="block w-full truncate text-left hover:text-accent" title="Open">
        {text}
      </button>
    );
  }
  return body;
}

/** One cell, in full, when the column is too narrow to be honest about it. */
function CellSheet({ column, value, onClose }: { column: Column; value: Cell; onClose: () => void }) {
  const text = value === null || value === undefined
    ? "NULL"
    : typeof value === "object"
      ? JSON.stringify(value, null, 2)
      : String(value);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[80vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-border bg-surface shadow-float"
      >
        <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
          <div className="min-w-0">
            <div className="truncate text-sm font-medium">{column.name}</div>
            <div className="font-mono text-[11px] text-ink-faint">{column.type}</div>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void navigator.clipboard?.writeText(text)}
              className="rounded-md border border-border px-2 py-1 text-xs text-ink-muted hover:bg-surface-2 hover:text-ink"
            >
              Copy
            </button>
            <button type="button" onClick={onClose} className="rounded-md px-2 py-1 text-xs text-ink-muted hover:bg-surface-2 hover:text-ink">
              Close
            </button>
          </div>
        </div>
        <pre className="min-h-0 flex-1 overflow-auto p-4 font-mono text-xs whitespace-pre-wrap">{text}</pre>
      </div>
    </div>
  );
}

function bytesLabel(base64: string) {
  const n = Math.floor((base64.length * 3) / 4);
  return `${n} byte${n === 1 ? "" : "s"}`;
}

function compare(a: Cell, b: Cell, cls: Class): number {
  if (a === null || a === undefined) return b === null || b === undefined ? 0 : -1;
  if (b === null || b === undefined) return 1;
  if (cls === "number") return Number(a) - Number(b);
  if (cls === "decimal") {
    const x = Number(a);
    const y = Number(b);
    // A decimal arrives as a string for exactness; comparing as numbers is
    // fine for ordering and wrong for equality, which is not asked here.
    if (Number.isFinite(x) && Number.isFinite(y)) return x - y;
  }
  return String(a).localeCompare(String(b), undefined, { numeric: true });
}

// ---- widths, per result shape --------------------------------------------

function widthStore() {
  try { return JSON.parse(localStorage.getItem("islet.sql.widths") ?? "{}") as Record<string, Record<string, number>>; }
  catch { return {}; }
}

function loadWidths(key?: string) {
  if (!key) return {};
  return widthStore()[key] ?? {};
}

function saveWidths(key: string | undefined, w: Record<string, number>) {
  if (!key) return;
  try {
    const all = widthStore();
    all[key] = w;
    localStorage.setItem("islet.sql.widths", JSON.stringify(all));
  } catch { /* a remembered width is not worth an error */ }
}

// ---- export ---------------------------------------------------------------

export type ExportFormat = "csv" | "json" | "markdown" | "insert";

/** Render a result as text. Bounded by the row cap, which is the point (§12.4). */
export function exportResult(columns: Column[], rows: Cell[][], format: ExportFormat, table = "t"): string {
  const text = (v: Cell) => (v === null || v === undefined ? "" : typeof v === "object" ? JSON.stringify(v) : String(v));
  switch (format) {
    case "csv": {
      const esc = (v: Cell) => {
        const s = text(v);
        return /[",\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
      };
      return [columns.map((c) => esc(c.name)).join(","), ...rows.map((r) => r.map(esc).join(","))].join("\n");
    }
    case "json":
      return JSON.stringify(
        rows.map((r) => Object.fromEntries(columns.map((c, i) => [c.name, r[i] ?? null]))),
        null, 2,
      );
    case "markdown": {
      const head = `| ${columns.map((c) => c.name).join(" | ")} |`;
      const rule = `| ${columns.map(() => "---").join(" | ")} |`;
      const body = rows.map((r) => `| ${r.map((v) => text(v).replaceAll("|", "\\|")).join(" | ")} |`);
      return [head, rule, ...body].join("\n");
    }
    case "insert": {
      const lit = (v: Cell, cls: Class) => {
        if (v === null || v === undefined) return "NULL";
        if (cls === "number" || cls === "decimal") return String(v);
        if (cls === "bool") return v ? "TRUE" : "FALSE";
        const s = typeof v === "object" ? JSON.stringify(v) : String(v);
        return `'${s.replaceAll("'", "''")}'`;
      };
      const cols = columns.map((c) => quoteIdent(c.name)).join(", ");
      return rows
        .map((r) => `INSERT INTO ${quoteIdent(table)} (${cols}) VALUES (${r.map((v, i) => lit(v, columns[i].class)).join(", ")});`)
        .join("\n");
    }
  }
}

export function quoteIdent(name: string) {
  return /^[a-z_][a-z0-9_]*$/.test(name) ? name : `"${name.replaceAll('"', '""')}"`;
}
