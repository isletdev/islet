import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import {
  run, sql, SqlError,
  type Cell, type Column, type Connection, type HistoryEntry,
  type SavedQuery, type StatementResult, type Summary, type Table, type Tree,
} from "@/lib/sql";
import { dangerLabel, needsConfirmation, split, statementAt, toByte, toChar } from "@/lib/sqlsplit";
import { useAuth } from "@/lib/auth";
import { useDialog, failure } from "@/lib/dialogs";
import { Alert, Button, Input, Tab, Tabs } from "@/components/ui";
import {
  CloseIcon, CopyIcon, DatabasesIcon, RefreshIcon, SearchIcon, TrashIcon,
} from "@/components/icons";
import SqlEditor from "@/components/sql/SqlEditor";
import SchemaTree from "@/components/sql/SchemaTree";
import ResultsGrid, { exportResult, type ExportFormat } from "@/components/sql/ResultsGrid";
import TableView, { type TableFilter } from "@/components/sql/TableView";
import ConnectionForm from "@/components/sql/ConnectionForm";
import type { SQLNamespace } from "@codemirror/lang-sql";

/**
 * The SQL client, for one database.
 *
 * The database is the one in the URL, and there is no way to change it here:
 * you get here from a database and everything on the page belongs to it — the
 * tabs, their text, the history and the saved queries. Switching is going back
 * and opening a different one, which is the same gesture as opening this one.
 *
 * Three regions: the schema on the left, tabs and the editor on the right, and
 * what came back underneath.
 */

type PanelTab = "results" | "messages" | "history" | "saved";

interface QueryTab {
  id: string;
  kind: "query";
  title: string;
  doc: string;
  /** The dedicated connection this tab holds, so SET and BEGIN survive a run. */
  sessionId?: string;
  savedId?: string;
}

interface TableTab {
  id: string;
  kind: "table";
  title: string;
  /** The connection this table belongs to. A query tab has no such tie: the
      same SQL can be run anywhere, and a table cannot. */
  ref: string;
  schema: string;
  table: string;
  filter?: TableFilter | null;
}

type OpenTab = QueryTab | TableTab;

// Tabs are remembered per connection: the text in them is about that database
// and is meaningless, or wrong, against another.
const STORE = "islet.sql.state:";
const ROW_CAPS = [100, 1000, 10_000, 50_000];

export default function Sql() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const ask = useDialog();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();

  const ref = params.get("ref") ?? "";
  const [connection, setConnection] = useState<Connection | null>(null);
  const [tabs, setTabs] = useState<OpenTab[]>(() => restore(ref).tabs ?? [newQueryTab()]);
  const [activeTab, setActiveTab] = useState<string>(() => restore(ref).active ?? "");
  const [treeState, setTreeState] = useState<{ tree: Tree | null; loading: boolean; error: string | null }>(
    { tree: null, loading: false, error: null },
  );
  const [panel, setPanel] = useState<PanelTab>("results");
  const [results, setResults] = useState<StatementResult[]>([]);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [runId, setRunId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [saved, setSaved] = useState<SavedQuery[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [cursor, setCursor] = useState(0);
  const [rowCap, setRowCap] = useState(1000);
  const [inTx, setInTx] = useState(false);
  const [editing, setEditing] = useState(false);
  const [treeWidth, setTreeWidth] = useState(() => restore(ref).treeWidth ?? 280);
  const [editorHeight, setEditorHeight] = useState(() => restore(ref).editorHeight ?? 220);
  const [treeOpen, setTreeOpen] = useState(true);
  const [finder, setFinder] = useState(false);

  const engine = connection?.engine ?? "postgres";
  const tab = tabs.find((t) => t.id === activeTab) ?? tabs[0] ?? null;
  const queryTab = tab?.kind === "query" ? tab : null;

  // ---- what is under the cursor -------------------------------------------
  const statements = useMemo(
    () => (queryTab ? split(queryTab.doc, engine) : []),
    [queryTab, engine],
  );
  const current = useMemo(() => {
    if (!queryTab || statements.length === 0) return null;
    const s = statementAt(statements, toByte(queryTab.doc, cursor));
    if (!s) return null;
    return { statement: s, from: toChar(queryTab.doc, s.start), to: toChar(queryTab.doc, s.end) };
  }, [queryTab, statements, cursor]);

  // ---- loading ------------------------------------------------------------
  const loadConnection = useCallback(() => {
    if (!isAdmin || !ref) return;
    sql.connections()
      .then((list) => setConnection(list.find((c) => c.ref === ref) ?? null))
      .catch((e) => setError(failure(e)));
  }, [isAdmin, ref]);

  useEffect(() => { loadConnection(); }, [loadConnection]);

  const loadTree = useCallback((refresh = false) => {
    if (!ref) return;
    setTreeState((s) => ({ ...s, loading: true, error: null }));
    sql.schema(ref, refresh)
      .then((tree) => setTreeState({ tree, loading: false, error: null }))
      .catch((e) => setTreeState({ tree: null, loading: false, error: failure(e) }));
  }, [ref]);

  useEffect(() => { loadTree(false); }, [loadTree]);

  // Saved queries belong to the database they were written against.
  const loadSaved = useCallback(() => {
    if (!isAdmin || !ref) return;
    void sql.saved().then((all) => setSaved(all.filter((q) => q.connectionRef === ref))).catch(() => {});
  }, [isAdmin, ref]);

  useEffect(() => { loadSaved(); }, [loadSaved]);

  const loadHistory = useCallback(() => {
    void sql.history({ ref }).then(setHistory).catch(() => {});
  }, [ref]);

  useEffect(() => { if (panel === "history") loadHistory(); }, [panel, loadHistory]);

  // Remember where you were. A query editor that forgets the query on a reload
  // is a query editor nobody trusts with anything longer than one line.
  useEffect(() => {
    try {
      if (ref) {
        localStorage.setItem(STORE + ref, JSON.stringify({
          tabs, active: activeTab, treeWidth, editorHeight,
        }));
      }
    } catch { /* a remembered tab is not worth an error */ }
  }, [ref, tabs, activeTab, treeWidth, editorHeight]);

  // A tab's session is closed when the tab is, and the daemon closes it anyway
  // after ten idle minutes: the unload does not have to arrive.
  useEffect(() => {
    const ids = () => tabs.filter((t): t is QueryTab => t.kind === "query").map((t) => t.sessionId).filter(Boolean) as string[];
    const bye = () => {
      for (const id of ids()) {
        navigator.sendBeacon?.(`/api/v1/sql/sessions/${id}`) ||
          void sql.closeSession(id).catch(() => {});
      }
    };
    window.addEventListener("pagehide", bye);
    return () => window.removeEventListener("pagehide", bye);
  }, [tabs]);

  // Arriving with something to open: ?table=schema.name for a table, ?sql=… to
  // land in the editor. The slow-query list on the Databases page uses the
  // second one, which is the loop no standalone client can close: find a slow
  // query, understand it, fix it, without leaving.
  useEffect(() => {
    const wantTable = params.get("table");
    const wantSQL = params.get("sql");
    if (!wantTable && !wantSQL) return;
    if (wantTable) {
      const [schema, ...rest] = wantTable.split(".");
      if (rest.length) openTable(schema, rest.join("."));
    }
    if (wantSQL) addQueryTab(wantSQL);
    const next = new URLSearchParams(params);
    next.delete("table");
    next.delete("sql");
    setParams(next, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  // ---- completion from the introspection cache ----------------------------
  const completionSchema = useMemo<SQLNamespace | undefined>(() => {
    const tree = treeState.tree;
    if (!tree) return undefined;
    const out: Record<string, string[]> = {};
    const single = tree.schemas.length === 1;
    for (const s of tree.schemas) {
      for (const t of s.tables) {
        const cols = (t.columns ?? []).map((c) => c.name);
        out[`${s.name}.${t.name}`] = cols;
        // With one schema, "customers" should complete as readily as
        // "public.customers", which is how people actually type.
        if (single) out[t.name] = cols;
      }
    }
    return out;
  }, [treeState.tree]);

  const defaultSchema = treeState.tree?.schemas[0]?.name;

  // ---- running ------------------------------------------------------------
  // Every run on a query tab goes through that tab's own connection, so BEGIN,
  // SET and a temporary table survive to the next run — and so an open
  // transaction is never left on a connection that goes back to the pool.
  const ensureSession = useCallback(async (t: QueryTab): Promise<string | undefined> => {
    if (t.sessionId) return t.sessionId;
    try {
      const s = await sql.openSession(ref);
      setTabs((all) => all.map((x) => (x.id === t.id && x.kind === "query" ? { ...x, sessionId: s.id } : x)));
      return s.id;
    } catch {
      // Without one a run still works; it just cannot hold a transaction open.
      return undefined;
    }
  }, [ref]);

  const doRun = useCallback(async (document: string) => {
    if (!ref || !queryTab || !document.trim()) return;
    setError(null);
    setResults([]);
    setSummary(null);
    setPanel("results");

    // 8.2: the daemon refuses a flagged statement that was not confirmed, and
    // this is where the confirmation is collected — naming the connection,
    // because a DELETE on production and a DELETE on a scratch copy are the
    // same statement.
    const stmts = split(document, engine);
    const risky = stmts.filter((s) => needsConfirmation(s));
    let confirmed = false;
    if (risky.length > 0) {
      const worst = risky[0];
      const reasons = worst.danger.map(dangerLabel);
      const ok = await ask.confirm({
        title: risky.length === 1 ? "Run this statement?" : `Run ${risky.length} statements that change things?`,
        body: (
          <div className="space-y-2">
            <p>On <span className="font-medium">{connection?.name}</span>.</p>
            <pre className="max-h-32 overflow-auto rounded-md border border-border bg-surface-2 p-2 font-mono text-[11px] whitespace-pre-wrap">{worst.sql}</pre>
            <ul className="list-disc pl-5 text-xs">{reasons.map((r) => <li key={r}>{r}</li>)}</ul>
          </div>
        ),
        confirmLabel: "Run it",
        tone: "danger",
        typeToConfirm: worst.danger.some((d) => d === "drop" || d === "truncate") ? connection?.name : undefined,
      });
      if (!ok) return;
      confirmed = true;
    }

    // Busy starts here, not before the dialog: a results panel that says
    // "Running…" while it is waiting for someone to type a name is lying.
    setBusy(true);

    const sessionId = await ensureSession(queryTab);
    const id = crypto.randomUUID().replaceAll("-", "");
    setRunId(id);
    const collected: StatementResult[] = [];
    try {
      await run(ref, document, {
        sessionId,
        runId: id,
        rowCap,
        confirmed,
      }, {
        onStatement: (r) => { collected.push(r); setResults([...collected]); },
        onEnd: (s) => {
          setSummary(s);
          setInTx(s.inTransaction);
          if (s.schemaChanged) loadTree(true);
          if (s.failed > 0) setPanel("messages");
        },
        onError: (m) => setError(m),
      });
    } catch (e) {
      const msg = e instanceof SqlError ? e.message : failure(e);
      setError(msg);
      setPanel("messages");
    } finally {
      setBusy(false);
      setRunId(null);
      loadHistory();
    }
  }, [ref, queryTab, engine, connection, ask, ensureSession, rowCap, loadTree, loadHistory]);

  const runStatement = () => { if (current) void doRun(current.statement.sql); };
  const runAll = () => { if (queryTab) void doRun(queryTab.doc); };

  const cancel = async () => {
    if (!runId || !ref) return;
    try { await sql.cancel(ref, runId); } catch (e) { void ask.alert({ title: "Could not cancel", body: failure(e), tone: "danger" }); }
  };

  const endTransaction = (how: "COMMIT" | "ROLLBACK") => doRun(how);

  // ---- tabs ---------------------------------------------------------------
  function openTable(schema: string, table: string, filter?: TableFilter) {
    const id = `t:${ref}:${schema}.${table}`;
    setTabs((all) => {
      const existing = all.find((t) => t.id === id);
      if (existing) {
        return all.map((t) => (t.id === id && t.kind === "table" ? { ...t, filter: filter ?? null } : t));
      }
      return [...all, { id, kind: "table", title: table, ref, schema, table, filter: filter ?? null }];
    });
    setActiveTab(id);
  }

  const addQueryTab = (doc = "") => {
    const t = newQueryTab(doc);
    setTabs((all) => [...all, t]);
    setActiveTab(t.id);
  };

  const closeTab = (id: string) => {
    const t = tabs.find((x) => x.id === id);
    if (t?.kind === "query" && t.sessionId) void sql.closeSession(t.sessionId).catch(() => {});
    setTabs((all) => {
      const next = all.filter((x) => x.id !== id);
      if (next.length === 0) return [newQueryTab()];
      return next;
    });
    setActiveTab((a) => (a === id ? (tabs.find((x) => x.id !== id)?.id ?? "") : a));
  };

  const setDoc = (doc: string) => {
    if (!queryTab) return;
    setTabs((all) => all.map((t) => (t.id === queryTab.id && t.kind === "query" ? { ...t, doc } : t)));
  };

  const intoEditor = (text: string) => {
    if (queryTab) { setDoc(text); setActiveTab(queryTab.id); }
    else addQueryTab(text);
  };

  const saveQuery = async () => {
    if (!queryTab || !queryTab.doc.trim()) return;
    const name = await ask.prompt({
      title: queryTab.savedId ? "Rename this query" : "Save this query",
      label: "Name",
      defaultValue: queryTab.savedId ? queryTab.title : "",
      placeholder: "Orders per customer",
      confirmLabel: "Save",
    });
    if (!name) return;
    try {
      const body = { name, description: "", sql: queryTab.doc, connectionRef: ref, engine };
      if (queryTab.savedId) await sql.updateSaved(queryTab.savedId, body);
      else {
        const created = await sql.save(body);
        setTabs((all) => all.map((t) => (t.id === queryTab.id && t.kind === "query" ? { ...t, savedId: created.id, title: name } : t)));
      }
      loadSaved();
      setPanel("saved");
    } catch (e) {
      void ask.alert({ title: "Could not save it", body: failure(e), tone: "danger" });
    }
  };

  if (!isAdmin) {
    return (
      <div className="mx-auto max-w-2xl">
        <Alert>Only admins can use the SQL client: arbitrary SQL is equivalent to root on the data.</Alert>
      </div>
    );
  }

  // The database is named in the URL. There is no picker here on purpose: you
  // arrive from a database, and everything on the page belongs to that one.
  if (!ref) {
    return (
      <div className="mx-auto max-w-2xl rounded-lg border border-dashed border-border p-8 text-center">
        <p className="text-sm text-ink-muted">Open a database to query it.</p>
        <Link
          to="/databases"
          className="mt-3 inline-flex h-8 items-center rounded-md bg-ink px-3 text-xs font-medium text-on-ink hover:opacity-90"
        >
          Go to Databases
        </Link>
      </div>
    );
  }

  return (
    <div className="-m-4 flex h-[calc(100vh-3.5rem)] min-h-0 flex-col md:-m-6">
      {/* ---- the bar that says where you are ---- */}
      <header className="flex flex-wrap items-center gap-2 border-b border-border bg-surface px-3 py-2">
        <button
          type="button"
          onClick={() => setTreeOpen((o) => !o)}
          className="rounded-md p-1.5 text-ink-muted hover:bg-surface-2 hover:text-ink lg:hidden"
          aria-label={treeOpen ? "Hide the schema" : "Show the schema"}
        >
          <DatabasesIcon className="h-4 w-4" />
        </button>

        {/* No sidebar entry leads here, so nothing in it says where you are. */}
        <Link
          to="/databases"
          className="-my-1 hidden shrink-0 items-center gap-1 py-1 text-xs text-ink-muted hover:text-ink sm:flex"
        >
          ← Databases
        </Link>

        <span className="min-w-0 truncate text-sm font-semibold">{connection?.name ?? "\u2026"}</span>
        {connection && (
          <>
            <span className="hidden font-mono text-[11px] text-ink-muted sm:inline">
              {connection.engine} · {connection.host}:{connection.port}
              {connection.database ? "/" + connection.database : ""}
            </span>
            {connection.readOnly && (
              <span className="rounded-sm bg-surface-2 px-1.5 py-0.5 text-[11px] text-ink-muted">read-only</span>
            )}
            {!connection.managed && (
              <Button variant="secondary" className="ml-auto h-8 px-2 text-xs" onClick={() => setEditing(true)}>
                Edit the connection
              </Button>
            )}
          </>
        )}
      </header>

      {inTx && (
        <div className="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning-soft px-3 py-1.5 text-xs text-warning">
          <span>A transaction is open on this tab. Nothing is permanent until you commit.</span>
          <div className="ml-auto flex gap-1.5">
            <Button className="h-6 px-2 text-[11px]" onClick={() => void endTransaction("COMMIT")}>Commit</Button>
            <Button variant="secondary" className="h-6 px-2 text-[11px]" onClick={() => void endTransaction("ROLLBACK")}>Roll back</Button>
          </div>
        </div>
      )}

      <div className="flex min-h-0 flex-1">
        {/* ---- the schema ---- */}
        <aside
          style={{ width: treeOpen ? treeWidth : 0 }}
          className={`flex min-h-0 shrink-0 flex-col border-r border-border bg-surface ${treeOpen ? "" : "hidden"}`}
        >
          <SchemaTree
            tree={treeState.tree}
            loading={treeState.loading}
            error={treeState.error}
            onRefresh={() => loadTree(true)}
            onOpenTable={(t: Table) => openTable(t.schema, t.name)}
            active={tab?.kind === "table" ? { schema: tab.schema, name: tab.table } : null}
          />
        </aside>
        {treeOpen && (
          <div
            role="separator"
            aria-orientation="vertical"
            onMouseDown={(e) => drag(e.clientX, treeWidth, (w) => setTreeWidth(clamp(w, 180, 520)))}
            className="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-accent/40"
          />
        )}

        {/* ---- tabs, editor, results ---- */}
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex items-center gap-1 overflow-x-auto border-b border-border bg-surface px-2">
            {tabs.map((t) => (
              <div
                key={t.id}
                className={`group flex shrink-0 items-center gap-1 border-b-2 px-2 py-1.5 text-xs ${
                  t.id === tab?.id ? "border-accent text-ink" : "border-transparent text-ink-muted hover:text-ink"
                }`}
              >
                <button type="button" onClick={() => setActiveTab(t.id)} className="-my-1 max-w-[16ch] truncate py-1">
                  {t.kind === "table" ? t.title : t.title || "Query"}
                </button>
                <button
                  type="button"
                  onClick={() => closeTab(t.id)}
                  aria-label={`Close ${t.title}`}
                  className="rounded-sm p-0.5 text-ink-faint opacity-0 hover:text-danger focus:opacity-100 group-hover:opacity-100"
                >
                  <CloseIcon className="h-3 w-3" />
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => addQueryTab()}
              className="shrink-0 rounded-md px-2 py-1 text-sm text-ink-muted hover:bg-surface-2 hover:text-ink"
              aria-label="New query tab"
            >
              +
            </button>
          </div>

          {tab?.kind === "table" ? (
            <TableView
              key={tab.id + (tab.filter?.value ?? "")}
              connectionRef={ref}
              engine={engine}
              schema={tab.schema}
              table={tab.table}
              initialFilter={tab.filter}
              onOpenTable={openTable}
              onOpenInEditor={(text) => { addQueryTab(text); }}
            />
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-1.5 border-b border-border px-3 py-1.5">
                <Button className="h-7 px-2 text-xs" onClick={runStatement} disabled={busy || !current}>
                  Run statement
                  <kbd className="ml-1.5 hidden font-mono text-[10px] opacity-60 sm:inline">⌘⏎</kbd>
                </Button>
                <Button variant="secondary" className="h-7 px-2 text-xs" onClick={runAll} disabled={busy || statements.length === 0}>
                  Run all {statements.length > 1 ? `(${statements.length})` : ""}
                </Button>
                {busy && (
                  <Button variant="danger" className="h-7 px-2 text-xs" onClick={() => void cancel()}>Cancel</Button>
                )}
                <Button variant="secondary" className="h-7 gap-1.5 px-2 text-xs" onClick={() => void saveQuery()} disabled={!queryTab?.doc.trim()}>
                  Save
                </Button>

                <select
                  value={rowCap}
                  onChange={(e) => setRowCap(Number(e.target.value))}
                  aria-label="How many rows to fetch"
                  title="A SELECT stops at this many rows, so one on a large table is boring rather than fatal."
                  className="ml-auto h-7 rounded-md border border-border bg-bg px-1.5 text-[11px]"
                >
                  {ROW_CAPS.map((n) => <option key={n} value={n}>{n.toLocaleString()} rows</option>)}
                </select>
              </div>

              <SqlEditor
                value={queryTab?.doc ?? ""}
                engine={engine}
                schema={completionSchema}
                defaultSchema={defaultSchema}
                current={current ? { from: current.from, to: current.to } : null}
                onChange={setDoc}
                onCursor={setCursor}
                shortcuts={{
                  onRunStatement: runStatement,
                  onRunAll: runAll,
                  onSave: () => void saveQuery(),
                  onSearchSchema: () => setFinder(true),
                }}
                className="shrink-0"
                style={{ height: editorHeight }}
              />

              <div
                role="separator"
                aria-orientation="horizontal"
                onMouseDown={(e) => dragY(e.clientY, editorHeight, (h) => setEditorHeight(clamp(h, 80, 640)))}
                className="h-1 shrink-0 cursor-row-resize bg-transparent hover:bg-accent/40"
              />

              <section className="flex min-h-0 flex-1 flex-col border-t border-border">
                <Tabs label="Results, messages, history and saved queries" className="px-2">
                  <Tab active={panel === "results"} onClick={() => setPanel("results")}>
                    Results{results.length > 1 ? ` (${results.length})` : ""}
                  </Tab>
                  <Tab active={panel === "messages"} onClick={() => setPanel("messages")}>Messages</Tab>
                  <Tab active={panel === "history"} onClick={() => setPanel("history")}>History</Tab>
                  <Tab active={panel === "saved"} onClick={() => setPanel("saved")}>Saved</Tab>
                </Tabs>

                <div className="flex min-h-0 flex-1 flex-col">
                  {panel === "results" && <ResultsPanel results={results} connectionRef={ref} busy={busy} />}
                  {panel === "messages" && (
                    <MessagesPanel results={results} summary={summary} error={error} />
                  )}
                  {panel === "history" && (
                    <HistoryPanel
                      entries={history}
                      onUse={intoEditor}
                      onRefresh={loadHistory}
                      onClear={async () => {
                        const ok = await ask.confirm({
                          title: "Clear the query history?",
                          body: "Every statement recorded on this server is removed. The audit log keeps its own record.",
                          confirmLabel: "Clear it",
                          tone: "danger",
                        });
                        if (!ok) return;
                        await sql.clearHistory();
                        loadHistory();
                      }}
                    />
                  )}
                  {panel === "saved" && (
                    <SavedPanel
                      queries={saved}
                      onUse={(q) => intoEditor(q.sql)}
                      onDelete={async (q) => {
                        const ok = await ask.confirm({
                          title: `Delete “${q.name}”?`,
                          confirmLabel: "Delete",
                          tone: "danger",
                        });
                        if (!ok) return;
                        await sql.deleteSaved(q.id);
                        loadSaved();
                      }}
                    />
                  )}
                </div>
              </section>
            </>
          )}
        </div>
      </div>

      {editing && connection && (
        <ConnectionForm
          connection={connection}
          onClose={() => setEditing(false)}
          onSaved={() => { setEditing(false); loadConnection(); loadTree(true); }}
          onDeleted={() => { setEditing(false); navigate("/databases"); }}
        />
      )}

      {finder && treeState.tree && (
        <SchemaFinder
          connectionRef={ref}
          onClose={() => setFinder(false)}
          onPick={(m) => {
            setFinder(false);
            if (m.table) openTable(m.schema, m.table);
          }}
        />
      )}
    </div>
  );
}

// ---- panels ---------------------------------------------------------------

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-1 items-center justify-center p-4 text-center text-xs text-ink-muted">{children}</div>;
}

function ResultsPanel({ results, connectionRef, busy }: { results: StatementResult[]; connectionRef: string; busy: boolean }) {
  const [which, setWhich] = useState(0);
  useEffect(() => { setWhich(Math.max(0, results.length - 1)); }, [results.length]);
  const withRows = results.filter((r) => (r.columns ?? []).length > 0);
  const r = withRows[Math.min(which, withRows.length - 1)] ?? results[results.length - 1];

  if (!r) return <Empty>{busy ? "Running…" : "Run something to see it here."}</Empty>;
  if (!r.columns || r.columns.length === 0) {
    return (
      <Empty>
        {r.error
          ? r.error.message
          : `${r.rowsAffected >= 0 ? r.rowsAffected : 0} row${r.rowsAffected === 1 ? "" : "s"} affected in ${r.durationMs} ms.`}
      </Empty>
    );
  }

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-1 text-[11px] text-ink-muted">
        {withRows.length > 1 && (
          <select
            value={which}
            onChange={(e) => setWhich(Number(e.target.value))}
            className="h-6 rounded-md border border-border bg-bg px-1 text-[11px]"
          >
            {withRows.map((s, i) => (
              <option key={i} value={i}>{`${i + 1}. ${firstWords(s.sql)}`}</option>
            ))}
          </select>
        )}
        <span>{r.rowCount.toLocaleString()} rows · {r.durationMs} ms</span>
        {r.truncated && (
          <span className="rounded-sm bg-warning-soft px-1.5 py-0.5 text-warning">
            stopped at the row cap — add a LIMIT or raise it to see the rest
          </span>
        )}
        <div className="ml-auto flex items-center gap-1">
          <CopyMenu columns={r.columns} rows={r.rows ?? []} />
        </div>
      </div>
      <ResultsGrid
        columns={r.columns}
        rows={r.rows ?? []}
        widthKey={`${connectionRef}:result:${r.columns.map((c) => c.name).join(",")}`}
        className="flex-1"
      />
    </>
  );
}

function CopyMenu({ columns, rows }: { columns: Column[]; rows: Cell[][] }) {
  const [open, setOpen] = useState(false);
  const copy = async (f: ExportFormat) => {
    setOpen(false);
    try { await navigator.clipboard.writeText(exportResult(columns, rows, f)); } catch { /* no clipboard */ }
  };
  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 hover:bg-surface-2 hover:text-ink"
      >
        <CopyIcon className="h-3 w-3" />Copy
      </button>
      {open && (
        <div className="absolute right-0 top-full z-20 mt-1 w-36 overflow-hidden rounded-md border border-border bg-surface shadow-float">
          {(["csv", "json", "markdown", "insert"] as ExportFormat[]).map((f) => (
            <button
              key={f}
              type="button"
              onClick={() => void copy(f)}
              className="block w-full px-2.5 py-1.5 text-left hover:bg-surface-2 hover:text-ink"
            >
              {f === "insert" ? "INSERT statements" : f.toUpperCase()}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function MessagesPanel({ results, summary, error }: { results: StatementResult[]; summary: Summary | null; error: string | null }) {
  if (!error && results.length === 0) return <Empty>Nothing has run yet.</Empty>;
  return (
    <div className="min-h-0 flex-1 overflow-auto p-3 text-xs">
      {error && <p className="mb-2 rounded-md bg-danger-soft px-2 py-1.5 text-danger">{error}</p>}
      {results.map((r, i) => (
        <div key={i} className="border-b border-border py-1.5 last:border-0">
          <div className="flex flex-wrap items-baseline gap-2">
            <span className="font-mono text-[11px] text-ink-faint">line {r.line}</span>
            <span className={`font-mono text-[11px] ${r.error ? "text-danger" : "text-ink-muted"}`}>{firstWords(r.sql)}</span>
            <span className="ml-auto text-[11px] text-ink-faint">{r.durationMs} ms</span>
          </div>
          {r.error ? (
            <div className="mt-1 text-danger">
              <p>{r.error.message}</p>
              {r.error.detail && <p className="text-ink-muted">{r.error.detail}</p>}
              {r.error.hint && <p className="text-ink-muted">Hint: {r.error.hint}</p>}
              {r.error.code && <p className="font-mono text-[11px] text-ink-faint">{r.error.code}</p>}
            </div>
          ) : (
            <p className="mt-0.5 text-ink-muted">
              {(r.columns ?? []).length > 0
                ? `${r.rowCount.toLocaleString()} row${r.rowCount === 1 ? "" : "s"}${r.truncated ? " (capped)" : ""}`
                : `${r.rowsAffected >= 0 ? r.rowsAffected : 0} row${r.rowsAffected === 1 ? "" : "s"} affected`}
            </p>
          )}
        </div>
      ))}
      {summary && (
        <p className="mt-2 text-ink-muted">
          {summary.statements} statement{summary.statements === 1 ? "" : "s"}, {summary.failed} failed, {summary.durationMs} ms.
          {summary.inTransaction ? " A transaction is open." : ""}
        </p>
      )}
    </div>
  );
}

function HistoryPanel({
  entries, onUse, onRefresh, onClear,
}: {
  entries: HistoryEntry[];
  onUse: (sql: string) => void;
  onRefresh: () => void;
  onClear: () => void;
}) {
  const [q, setQ] = useState("");
  const shown = entries.filter((e) => !q || e.sql.toLowerCase().includes(q.toLowerCase()));
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search the history" className="h-7 w-56 text-xs" />
        <Button variant="secondary" className="h-7 gap-1.5 px-2 text-xs" onClick={onRefresh}>
          <RefreshIcon className="h-3.5 w-3.5" />Refresh
        </Button>
        <Button variant="danger" className="ml-auto h-7 gap-1.5 px-2 text-xs" onClick={onClear}>
          <TrashIcon className="h-3.5 w-3.5" />Clear
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {shown.length === 0 && <Empty>Nothing here yet.</Empty>}
        {shown.map((e) => (
          <button
            key={e.id}
            type="button"
            onClick={() => onUse(e.sql)}
            className="block w-full border-b border-border px-3 py-1.5 text-left hover:bg-surface-2"
          >
            <div className="flex flex-wrap items-baseline gap-2 text-[11px] text-ink-faint">
              <span>{new Date(e.startedAt).toLocaleString()}</span>
              <span>{e.actor}</span>
              <span>{e.durationMs} ms</span>
              <span>{e.rowCount} rows{e.truncated ? " (capped)" : ""}</span>
              {e.error && <span className="text-danger">failed</span>}
            </div>
            <code className="mt-0.5 block truncate font-mono text-[11px]">{firstWords(e.sql, 160)}</code>
          </button>
        ))}
      </div>
    </div>
  );
}

function SavedPanel({
  queries, onUse, onDelete,
}: {
  queries: SavedQuery[];
  onUse: (q: SavedQuery) => void;
  onDelete: (q: SavedQuery) => void;
}) {
  if (queries.length === 0) return <Empty>Save a query and it will be here, on the server, on any machine you sign in from.</Empty>;
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      {queries.map((q) => (
        <div key={q.id} className="flex items-start gap-2 border-b border-border px-3 py-1.5 hover:bg-surface-2">
          <button type="button" onClick={() => onUse(q)} className="min-w-0 flex-1 text-left">
            <div className="flex flex-wrap items-baseline gap-2">
              <span className="text-xs font-medium">{q.name}</span>
              <span className="text-[11px] text-ink-faint">{q.createdBy}</span>
              {q.params.length > 0 && (
                <span className="font-mono text-[10px] text-accent">:{q.params.join(" :")}</span>
              )}
            </div>
            <code className="mt-0.5 block truncate font-mono text-[11px] text-ink-muted">{firstWords(q.sql, 160)}</code>
          </button>
          <button
            type="button"
            onClick={() => onDelete(q)}
            aria-label={`Delete ${q.name}`}
            className="shrink-0 rounded-md p-1 text-ink-faint hover:text-danger"
          >
            <TrashIcon className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}

/** The schema search (7.3), on the shortcut the editor announces. */
function SchemaFinder({
  connectionRef, onClose, onPick,
}: {
  connectionRef: string;
  onClose: () => void;
  onPick: (m: { schema: string; table?: string; column?: string }) => void;
}) {
  const [q, setQ] = useState("");
  const [matches, setMatches] = useState<{ kind: string; schema: string; table?: string; column?: string; type?: string; label: string }[]>([]);
  const box = useRef<HTMLInputElement>(null);

  useEffect(() => { box.current?.focus(); }, []);
  useEffect(() => {
    if (!q.trim()) { setMatches([]); return; }
    let alive = true;
    const t = setTimeout(() => {
      void sql.search(connectionRef, q).then((m) => alive && setMatches(m)).catch(() => {});
    }, 90);
    return () => { alive = false; clearTimeout(t); };
  }, [q, connectionRef]);

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-24" onClick={onClose}>
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-lg overflow-hidden rounded-lg border border-border bg-surface shadow-float"
      >
        <div className="flex items-center gap-2 border-b border-border px-3">
          <SearchIcon className="h-4 w-4 shrink-0 text-ink-faint" />
          <input
            ref={box}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Escape") onClose(); if (e.key === "Enter" && matches[0]) onPick(matches[0]); }}
            placeholder="Find a table or a column"
            className="h-11 flex-1 bg-transparent text-sm outline-none placeholder:text-ink-faint"
          />
        </div>
        <div className="max-h-80 overflow-auto">
          {matches.map((m, i) => (
            <button
              key={i}
              type="button"
              onClick={() => onPick(m)}
              className="flex w-full items-baseline gap-2 px-3 py-1.5 text-left text-xs hover:bg-surface-2"
            >
              <span className="w-12 shrink-0 text-[10px] uppercase text-ink-faint">{m.kind}</span>
              <span className="truncate">{m.label}</span>
              {m.type && <span className="ml-auto shrink-0 font-mono text-[10px] text-ink-faint">{m.type}</span>}
            </button>
          ))}
          {q && matches.length === 0 && <p className="px-3 py-4 text-center text-xs text-ink-muted">Nothing matches.</p>}
        </div>
      </div>
    </div>
  );
}

// ---- small things ---------------------------------------------------------

function newQueryTab(doc = ""): QueryTab {
  return { id: "q:" + Math.random().toString(36).slice(2, 9), kind: "query", title: "Query", doc };
}

function firstWords(s: string, n = 70) {
  const one = s.replace(/\s+/g, " ").trim();
  return one.length > n ? one.slice(0, n - 1) + "…" : one;
}

function clamp(n: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, n));
}

function drag(startX: number, startW: number, set: (w: number) => void) {
  const move = (e: MouseEvent) => set(startW + (e.clientX - startX));
  const up = () => { document.removeEventListener("mousemove", move); document.removeEventListener("mouseup", up); };
  document.addEventListener("mousemove", move);
  document.addEventListener("mouseup", up);
}

function dragY(startY: number, startH: number, set: (h: number) => void) {
  const move = (e: MouseEvent) => set(startH + (e.clientY - startY));
  const up = () => { document.removeEventListener("mousemove", move); document.removeEventListener("mouseup", up); };
  document.addEventListener("mousemove", move);
  document.addEventListener("mouseup", up);
}

interface Stored {
  tabs?: OpenTab[];
  active?: string;
  treeWidth?: number;
  editorHeight?: number;
}

function restore(ref: string): Stored {
  if (!ref) return {};
  try {
    const v = JSON.parse(localStorage.getItem(STORE + ref) ?? "{}") as Stored;
    // A session id from a previous page load is gone: the daemon closed it.
    if (v.tabs) v.tabs = v.tabs.map((t) => (t.kind === "query" ? { ...t, sessionId: undefined } : t));
    return v;
  } catch {
    return {};
  }
}
