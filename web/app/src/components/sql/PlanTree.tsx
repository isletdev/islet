import { useMemo, useState } from "react";
import type { Plan } from "@/lib/sql";

/**
 * A query plan, read rather than decoded.
 *
 * Postgres and MySQL both return JSON, and both return it in their own shape.
 * What a person wants from it is the same either way: which node cost the
 * most, where the estimate was wrong, and what is scanning a whole table. Those
 * three are flagged; everything else is a tree you can open.
 */

interface Node {
  label: string;
  detail: string;
  rows: number;
  actualRows: number;
  cost: number;
  timeMs: number;
  /** Share of the plan's total cost, for the bar. */
  share: number;
  warnings: string[];
  children: Node[];
}

export default function PlanTree({ plan }: { plan: Plan }) {
  const root = useMemo(() => (plan.json ? build(plan) : null), [plan]);

  if (!root) {
    return (
      <div className="min-h-0 overflow-auto p-3">
        <pre className="font-mono text-xs whitespace-pre-wrap">{plan.text || "The server returned no plan."}</pre>
      </div>
    );
  }
  return (
    <div className="min-h-0 overflow-auto p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-[11px] text-ink-muted">
        <span className={`rounded-sm px-1.5 py-0.5 ${plan.analyzed ? "bg-warning-soft text-warning" : "bg-surface-2"}`}>
          {plan.analyzed ? "EXPLAIN ANALYZE — the statement was run" : "EXPLAIN — the statement was not run"}
        </span>
        <span>{plan.durationMs} ms</span>
      </div>
      <PlanNode node={root} depth={0} analyzed={plan.analyzed} />
    </div>
  );
}

function PlanNode({ node, depth, analyzed }: { node: Node; depth: number; analyzed: boolean }) {
  const [open, setOpen] = useState(depth < 3);
  const bad = node.warnings.length > 0;
  return (
    <div className={depth > 0 ? "ml-3 border-l border-border pl-3" : ""}>
      <div className="py-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          {node.children.length > 0 && (
            <button
              type="button"
              onClick={() => setOpen((o) => !o)}
              className="text-ink-faint hover:text-ink"
              aria-label={open ? "Collapse" : "Expand"}
            >
              {open ? "−" : "+"}
            </button>
          )}
          <span className={`text-[13px] font-medium ${bad ? "text-warning" : ""}`}>{node.label}</span>
          {node.detail && <span className="truncate font-mono text-[11px] text-ink-muted">{node.detail}</span>}
          <span className="ml-auto flex shrink-0 items-baseline gap-3 font-mono text-[11px] tabular-nums text-ink-faint">
            {analyzed && node.timeMs > 0 && <span title="Actual time">{node.timeMs.toFixed(1)} ms</span>}
            <span title={analyzed ? "estimated → actual rows" : "estimated rows"}>
              {fmt(node.rows)}{analyzed && node.actualRows >= 0 ? ` → ${fmt(node.actualRows)}` : ""}
            </span>
            {node.cost > 0 && <span title="Total cost">{fmt(node.cost)}</span>}
          </span>
        </div>
        {node.share > 0 && (
          <div className="mt-1 h-1 w-full overflow-hidden rounded-full bg-surface-2">
            <div className={`h-full ${bad ? "bg-warning" : "bg-accent"}`} style={{ width: `${Math.min(100, node.share * 100).toFixed(1)}%` }} />
          </div>
        )}
        {node.warnings.map((wn) => (
          <p key={wn} className="mt-1 text-[11px] text-warning">{wn}</p>
        ))}
      </div>
      {open && node.children.map((c, i) => <PlanNode key={i} node={c} depth={depth + 1} analyzed={analyzed} />)}
    </div>
  );
}

function fmt(n: number) {
  if (!Number.isFinite(n) || n < 0) return "–";
  if (n < 1000) return String(Math.round(n));
  if (n < 1_000_000) return (n / 1000).toFixed(1) + "k";
  return (n / 1_000_000).toFixed(1) + "M";
}

// ---- reading each engine's own shape --------------------------------------

function build(plan: Plan): Node | null {
  const root = plan.engine === "mysql" || plan.engine === "mariadb" ? mysqlRoot(plan.json) : postgresRoot(plan.json);
  if (!root) return null;
  const total = maxCost(root);
  share(root, total);
  return root;
}

type Obj = Record<string, unknown>;

function num(o: Obj, ...keys: string[]): number {
  for (const k of keys) {
    const v = o[k];
    if (typeof v === "number") return v;
    if (typeof v === "string" && v !== "" && Number.isFinite(Number(v))) return Number(v);
  }
  return -1;
}

function postgresRoot(json: unknown): Node | null {
  // EXPLAIN (FORMAT JSON) is an array of one object with a "Plan".
  const first = Array.isArray(json) ? json[0] : json;
  const plan = (first as Obj | undefined)?.["Plan"];
  return plan ? pgNode(plan as Obj) : null;
}

function pgNode(o: Obj): Node {
  const label = String(o["Node Type"] ?? "Node");
  const rel = o["Relation Name"] ? String(o["Relation Name"]) : "";
  const alias = o["Alias"] && o["Alias"] !== rel ? ` ${o["Alias"]}` : "";
  const index = o["Index Name"] ? ` using ${o["Index Name"]}` : "";
  const rows = num(o, "Plan Rows");
  const actual = num(o, "Actual Rows");
  const loops = Math.max(1, num(o, "Actual Loops"));
  const timeMs = num(o, "Actual Total Time");

  const warnings: string[] = [];
  if (label.includes("Seq Scan") && rows > 10_000) {
    warnings.push(`A sequential scan over roughly ${fmt(rows)} rows. An index on the filtered column would turn this into a lookup.`);
  }
  const actualTotal = actual >= 0 ? actual * loops : -1;
  if (rows > 0 && actualTotal >= 0 && (actualTotal > rows * 10 || (actualTotal * 10 < rows && actualTotal > 0))) {
    warnings.push(`The estimate is out by more than ten times: planned ${fmt(rows)}, got ${fmt(actualTotal)}. ANALYZE this table.`);
  }
  if (o["Sort Space Type"] === "Disk") {
    warnings.push(`The sort spilled to disk (${fmt(num(o, "Sort Space Used"))} kB). More work_mem would keep it in memory.`);
  }

  const children = Array.isArray(o["Plans"]) ? (o["Plans"] as Obj[]).map(pgNode) : [];
  return {
    label,
    detail: [rel + alias, index, o["Filter"] ? `filter ${o["Filter"]}` : ""].filter(Boolean).join(" ").trim(),
    rows,
    actualRows: actualTotal,
    cost: num(o, "Total Cost"),
    timeMs: timeMs >= 0 ? timeMs : -1,
    share: 0,
    warnings,
    children,
  };
}

function mysqlRoot(json: unknown): Node | null {
  const o = json as Obj | undefined;
  const block = o?.["query_block"];
  return block ? myNode(block as Obj, "Query") : null;
}

function myNode(o: Obj, fallback: string): Node {
  const tbl = o["table"] as Obj | undefined;
  const source = tbl ?? o;
  const label = String(
    (source["access_type"] as string | undefined) ??
    (o["select_id"] !== undefined ? "Select" : fallback),
  );
  const name = source["table_name"] ? String(source["table_name"]) : "";
  const key = source["key"] ? ` using ${source["key"]}` : "";
  const rows = num(source, "rows_examined_per_scan", "rows_produced_per_join", "rows");
  const cost = num((source["cost_info"] as Obj) ?? {}, "query_cost", "read_cost", "prefix_cost");

  const warnings: string[] = [];
  if (label === "ALL" && rows > 10_000) {
    warnings.push(`A full table scan over roughly ${fmt(rows)} rows. An index on the filtered column would turn this into a lookup.`);
  }
  if (source["using_filesort"]) warnings.push("The rows are sorted after they are read; an index in that order would avoid it.");
  if (source["using_temporary_table"]) warnings.push("A temporary table is materialised for this step.");

  const children: Node[] = [];
  for (const k of ["nested_loop", "ordering_operation", "grouping_operation", "duplicates_removal", "materialized_from_subquery"]) {
    const v = o[k] ?? source[k];
    if (Array.isArray(v)) for (const c of v) children.push(myNode(c as Obj, k));
    else if (v && typeof v === "object") children.push(myNode(v as Obj, k));
  }
  return {
    label: label === "ALL" ? "Full table scan" : label,
    detail: [name, key, source["attached_condition"] ? `filter ${source["attached_condition"]}` : ""].filter(Boolean).join(" ").trim(),
    rows,
    actualRows: -1,
    cost,
    timeMs: -1,
    share: 0,
    warnings,
    children,
  };
}

function maxCost(n: Node): number {
  return Math.max(n.cost, ...n.children.map(maxCost));
}

function share(n: Node, total: number) {
  n.share = total > 0 && n.cost > 0 ? n.cost / total : 0;
  for (const c of n.children) share(c, total);
}

/** An index the plan suggests, as a statement to read before running (7.17). */
export function indexSuggestion(plan: Plan): string | null {
  const root = plan.json ? build(plan) : null;
  if (!root) return null;
  let found: { table: string; column: string } | null = null;
  const walk = (n: Node) => {
    if (!found && (n.label.includes("Seq Scan") || n.label === "Full table scan")) {
      const m = /filter\s+\(?\(?"?([a-zA-Z_][a-zA-Z0-9_]*)"?\s*[=><]/.exec(n.detail);
      const table = n.detail.split(/\s+/)[0];
      if (m && table && /^[a-zA-Z_][a-zA-Z0-9_.]*$/.test(table)) found = { table, column: m[1] };
    }
    n.children.forEach(walk);
  };
  walk(root);
  if (!found) return null;
  const { table, column } = found as { table: string; column: string };
  return `CREATE INDEX ${table.replace(".", "_")}_${column}_idx ON ${table} (${column});`;
}
