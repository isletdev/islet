import { useCallback, useMemo, useState } from "react";

/**
 * Sorting a table, the same way on every table that has one.
 *
 * A list you cannot order is a list you have to read all of. The rules here are
 * the ones people expect without being able to say so: clicking a column sorts
 * it, clicking again reverses, names compare naturally so `app-10` comes after
 * `app-9`, and a missing value sorts last in either direction rather than
 * pretending to be zero or an empty string.
 *
 * The choice is remembered per table, because the order you want in a file
 * browser is a habit rather than a decision you enjoy making again.
 */

export type Direction = "asc" | "desc";
export interface Sort<K extends string> { key: K; dir: Direction }

/** How a column's values compare. */
export type Kind = "text" | "number" | "date";

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

function compare(a: unknown, b: unknown, kind: Kind): number {
  // Nothing sorts last, whichever way the column is pointing: a row with no
  // size is not a row of size zero.
  const aEmpty = a === null || a === undefined || a === "";
  const bEmpty = b === null || b === undefined || b === "";
  if (aEmpty || bEmpty) return aEmpty && bEmpty ? 0 : aEmpty ? 1 : -1;

  switch (kind) {
    case "number":
      return Number(a) - Number(b);
    case "date": {
      const x = new Date(String(a)).getTime();
      const y = new Date(String(b)).getTime();
      if (Number.isNaN(x) || Number.isNaN(y)) return collator.compare(String(a), String(b));
      return x - y;
    }
    default:
      return collator.compare(String(a), String(b));
  }
}

export interface Column<T, K extends string> {
  key: K;
  kind?: Kind;
  /** The value to sort on; the cell may render something else entirely. */
  value: (row: T) => unknown;
}

/**
 * useSort returns the ordered rows and the props a header needs.
 *
 * `group` keeps rows in bands that sorting cannot break: folders stay above
 * files however the column is pointing, because a folder that sorts into the
 * middle of a file list is a file browser nobody can use.
 */
export function useSort<T, K extends string>(
  rows: T[],
  columns: Column<T, K>[],
  opts: { initial: Sort<K>; remember?: string; group?: (row: T) => number },
) {
  const [sort, setSort] = useState<Sort<K>>(() => restore(opts.remember) ?? opts.initial);

  const toggle = useCallback((key: K) => {
    setSort((s) => {
      const next: Sort<K> = s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: "asc" };
      remember(opts.remember, next);
      return next;
    });
  }, [opts.remember]);

  const sorted = useMemo(() => {
    const col = columns.find((c) => c.key === sort.key);
    if (!col) return rows;
    const sign = sort.dir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      if (opts.group) {
        const g = opts.group(a) - opts.group(b);
        if (g !== 0) return g;
      }
      return compare(col.value(a), col.value(b), col.kind ?? "text") * sign;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, columns, sort, opts.group]);

  return { rows: sorted, sort, toggle };
}

/** A column heading that sorts. */
export function SortHeader<K extends string>({
  label, column, sort, onSort, className = "", align = "left",
}: {
  label: string;
  column: K;
  sort: Sort<K>;
  onSort: (key: K) => void;
  className?: string;
  align?: "left" | "right";
}) {
  const active = sort.key === column;
  return (
    <th className={`py-2.5 font-medium ${align === "right" ? "text-right" : "text-left"} ${className}`}>
      <button
        type="button"
        onClick={() => onSort(column)}
        aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
        className={`-my-1 inline-flex items-center gap-1 py-1 ${align === "right" ? "flex-row-reverse" : ""} ${
          active ? "text-ink" : "hover:text-ink"
        }`}
      >
        {label}
        <span aria-hidden="true" className={`text-[9px] leading-none ${active ? "text-accent" : "text-ink-faint opacity-0 group-hover:opacity-100"}`}>
          {active ? (sort.dir === "asc" ? "▲" : "▼") : "▲"}
        </span>
      </button>
    </th>
  );
}

// ---- remembering the choice ----------------------------------------------

const KEY = "islet.sort";

function store(): Record<string, Sort<string>> {
  try { return JSON.parse(localStorage.getItem(KEY) ?? "{}") as Record<string, Sort<string>>; }
  catch { return {}; }
}

function restore<K extends string>(name?: string): Sort<K> | null {
  if (!name) return null;
  const v = store()[name];
  return v ? (v as Sort<K>) : null;
}

function remember<K extends string>(name: string | undefined, sort: Sort<K>) {
  if (!name) return;
  try {
    const all = store();
    all[name] = sort as Sort<string>;
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch { /* a remembered order is not worth an error */ }
}
