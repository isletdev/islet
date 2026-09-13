# The SQL client

A build specification. The reader is an engineer or an agent implementing this
from scratch inside the Islet repository. It states what to build, what not to
build, the decisions already made and why, and the bar the result has to clear.

**Status: shipped in v0.4.0.** Phases 1 to 3 of section 11 are complete, phase 4
is complete apart from inline editing, and `EXPLAIN` rendering and index
suggestions arrived from phase 5. Section 13 lists what is not built. Where this
document and `internal/sqlclient` disagree, the code is the fact and this is the
intent; reconcile them rather than letting them drift.

---

## 1. Why this exists

Islet installs databases, and today the only way to look inside one is Adminer:
a separate container, a separate login, and a tool that has not changed since
2010. The obvious upgrade, NocoDB, was evaluated and rejected: measured at
770 MB resident while idle, it stores your database credentials in its own
database, cannot open Redis or Mongo, and ships under the Sustainable Use
License, which is not open source. For a panel whose claim is that it stays out
of the way on a small server, that is the wrong trade.

The opportunity is that Islet already knows things a standalone client cannot.

- It installed the databases, so it has the host, port, user and password
  already. There is no "add connection" step for the common case.
- It has an encrypted secret store, a migration system, an audit log and a
  command log, so saved connections and history are a table, not a subsystem.
- It already bundles CodeMirror, lazy-loaded, so the editor costs nothing new.
- It runs as a daemon on the host, which can reach a container's bridge address
  directly. Nothing has to be published to the internet to query it.

The result should feel like part of the server, not a tool bolted onto it.

---

## 2. Non-goals

Say no to these explicitly. Each one has sunk a project like this before.

- **Dashboards and business intelligence.** Metabase exists and is in the
  catalog. One "chart this result" affordance is in scope; a dashboard builder,
  saved dashboards, scheduled reports and sharing are not.
- **A no-code app builder.** Not an Airtable clone. The audience is someone who
  knows SQL, or is learning it.
- **Mongo and Redis, in version one.** They are not relational and every
  feature here assumes tables, columns and foreign keys. Adminer stays in the
  catalog and remains the answer for those.
- **Collaborative editing, cloud sync, an account system.** Islet already has
  users and sessions. Use them.
- **Schema migration management.** Writing DDL is in scope. Versioned migration
  tooling is a different product.
- **Hiding SQL.** Every generated query is visible and editable. A filter in the
  table viewer shows the `WHERE` clause it produced. The tool should teach.

---

## 3. Decisions already made

These are settled. Revisit only with a reason written down.

### 3.1 Query with real drivers, not by shelling out

`internal/db.sql()` runs `psql -At -F tab` through `docker exec` and parses the
text. That is adequate for a console and a dead end here: text output carries no
column types, cannot distinguish `NULL` from an empty string, cannot stream, and
gives nothing to hang foreign-key navigation on.

Use:

- `github.com/jackc/pgx/v5` for PostgreSQL
- `github.com/go-sql-driver/mysql` for MySQL and MariaDB

Both are mature, pure Go, and do not need cgo, so the build stays
`CGO_ENABLED=0`. Expect roughly 3 to 4 MB on the binary. That is the price of
the whole feature and it is worth it.

The existing `docker exec` path stays where it is. It is still the right tool
for dumps, restores and engines the drivers do not cover.

### 3.2 Reach the database on its bridge address

The daemon runs on the host. On Linux the host can reach a container's bridge
address directly, which is what `db.Instance.IP` already holds and what the
tunnel hint on the Databases page already prints. Connect there. Nothing needs
publishing to the internet, and no firewall rule changes.

Handle the failure honestly: if the address is unreachable, say so and name the
likely cause rather than hanging. A container that was recreated gets a new
address, so resolve it at connection time, never cache it across restarts.

### 3.3 One instance, inside the daemon

No extra container, no extra image, no idle memory beyond what an open result
set holds. This is the entire argument against NocoDB and it must stay true. See
the performance budget in section 9.

### 3.4 Statement boundaries come from the parse tree

Do not split on semicolons. String literals, dollar-quoted blocks, comments and
procedure bodies all break that. Add `@codemirror/lang-sql`, which gives a real
syntax tree, and walk it to find statement ranges. "Run the statement under the
cursor" falls out of it, and so does schema-aware autocomplete.

The backend must also be able to split, because a request may carry several
statements. Implement it once in Go with a small lexer that understands string
literals, `$tag$` quoting, `--` and `/* */`, and test it against the nasty cases
in section 10. Do not trust the client's split: it decides what to send, the
server decides what it will run.

---

## 4. Data model

Three new tables. Follow the existing convention in `internal/store/migrations`:
one numbered file, forward only, one transaction.

### `sql_connections`

A saved connection. Databases Islet installed do not need a row here; they are
listed from `db.Service` at request time and referenced by instance name. Rows
exist for external databases the user added by hand.

| Column | Notes |
|---|---|
| `id` | text primary key |
| `server_id` | as every other table |
| `name` | what the user calls it |
| `engine` | `postgres` or `mysql` |
| `host`, `port`, `username`, `database` | plain |
| `password_enc` | encrypted with `auth.Keys`, never returned to the client |
| `tls_mode` | `disable`, `require`, `verify-full` |
| `read_only` | boolean, see 8.3 |
| `environment` | `development`, `staging`, `production`, see 7.14 |
| `created_at`, `created_by` | |

### `sql_saved_queries`

| Column | Notes |
|---|---|
| `id`, `server_id` | |
| `name`, `description` | |
| `sql` | the text |
| `connection_ref` | instance name or connection id, nullable |
| `params_json` | declared parameters, see 7.6 |
| `created_by`, `created_at`, `updated_at` | |

### `sql_history`

Every statement actually executed. This is the feature most clients do badly and
it is nearly free here.

| Column | Notes |
|---|---|
| `id`, `server_id` | |
| `connection_ref` | |
| `sql` | the statement as run |
| `actor` | which Islet user |
| `started_at`, `duration_ms` | |
| `row_count` | rows returned or affected |
| `error` | nullable |
| `truncated` | whether the row cap was hit |

Prune on a schedule: keep the last 10,000 rows per server, or 90 days,
whichever is smaller. A history table that grows forever is a bug.

---

## 5. Backend

New package `internal/sqlclient`. It owns connections, pools, introspection,
execution and cancellation. `internal/api/sqlclient.go` is the HTTP edge and
does nothing but validate, authorise and marshal.

### 5.1 Connection handling

- One pool per (connection, Islet user). Not per tab: a tab is a session on top
  of a pool, see below.
- Pool limits: maximum 4 connections per pool, idle timeout 5 minutes, whole
  pool closed after 30 minutes with no use. A panel must not exhaust a small
  database's connection limit.
- A **session** is what a tab holds: one dedicated connection from the pool, so
  `SET`, temporary tables and transactions behave the way the user expects.
  Sessions have their own idle timeout, 10 minutes, and are closed when the tab
  is closed. The frontend must send a close on unload and the backend must not
  depend on it arriving.
- Every query runs with a context that carries a timeout, and cancellation must
  reach the server: pgx sends a cancel request, MySQL needs an explicit
  `KILL QUERY` on a second connection. A cancel button that only abandons the
  response is not a cancel button.

### 5.2 Endpoints

All admin-only in version one. All under `/api/v1/sql`.

| Method and path | Purpose |
|---|---|
| `GET /connections` | Islet's own databases plus saved external ones, never with passwords |
| `POST /connections` | add an external connection; test it before saving |
| `PUT /connections/{id}`, `DELETE /connections/{id}` | |
| `POST /connections/{ref}/test` | connect, report version and latency, disconnect |
| `GET /connections/{ref}/schema` | the whole tree, cached, see 5.3 |
| `GET /connections/{ref}/table/{schema}/{table}` | columns, indexes, constraints, foreign keys both directions, row estimate |
| `POST /connections/{ref}/query` | run; streams results as SSE, see 5.4 |
| `POST /connections/{ref}/cancel` | cancel a running query by its id |
| `POST /connections/{ref}/explain` | run `EXPLAIN`, return the plan as JSON |
| `GET /history`, `DELETE /history` | |
| `GET /saved`, `POST /saved`, `PUT /saved/{id}`, `DELETE /saved/{id}` | |
| `POST /sessions`, `DELETE /sessions/{id}` | open and close a tab's session |

### 5.3 Introspection

This is the least glamorous part and everything good depends on it. For each
engine, gather:

- schemas, tables, views, materialised views
- columns with type, nullability, default, identity or auto-increment
- primary keys, unique constraints, check constraints
- foreign keys, **in both directions**: what this table points at, and what
  points at it. The reverse direction is what makes row navigation feel good
  and it is the half people forget.
- indexes with their columns and uniqueness
- approximate row counts, from `pg_class.reltuples` and
  `information_schema.tables.table_rows`. Never `COUNT(*)` a table to draw a
  tree.
- for Postgres, whether `pg_stat_statements` is installed, which
  `internal/db` already knows how to check

Cache per connection with a 60 second time to live and an explicit refresh.
Invalidate after any statement the splitter classified as DDL.

### 5.4 Executing

A run request carries: the SQL, the session id, a row cap, a statement timeout,
and whether to wrap in a transaction. The response is a stream, one message per
statement, so a script of ten statements reports each as it finishes rather than
going quiet for a minute.

Per statement, return: the statement text, its range in the original document so
the editor can highlight it, columns with names and types, rows up to the cap,
whether it was truncated, rows affected, duration, and any error with its
position so the editor can underline it.

Rules that are not negotiable:

- Never buffer more than the row cap. Stream from the driver into a bounded
  structure and stop.
- Values arrive typed. `NULL` is not an empty string. Numbers are not strings.
  JSON, arrays, dates, intervals, bytea and enums each need a decided
  representation, documented in the code.
- On error, stop the batch unless the user asked to continue, and say which
  statement failed.

---

## 6. Frontend

New page at `/sql`, lazy-loaded. Layout:

```
┌──────────────┬─────────────────────────────────────────┐
│  connections │  tabs                                   │
│  schema tree ├─────────────────────────────────────────┤
│  (resizable) │  editor                                 │
│              ├─────────────────────────────────────────┤
│              │  results · messages · plan · history    │
└──────────────┴─────────────────────────────────────────┘
```

- Panes resize by dragging and the sizes persist per user.
- The whole thing must work at 1280 wide. Below 900, the tree collapses to a
  drawer. It does not need to be good on a phone; it needs to not be broken.
- Follow `components/ui.tsx`. New primitives go there, not into the page.
- Use the existing `Tabs`, `Field`, `Select`, `Dialog` and `FolderPicker`
  patterns. Do not introduce a second way to do any of them.
- No new charting or grid library without a written reason. The grid is the one
  place a dependency might be justified; measure the bundle cost first.

---

## 7. Features

### Version one: the bar for calling it done

**7.1 Connections that are already there.** Every database Islet installed
appears in the list with no configuration. Clicking one connects. External
connections are added through a form that tests before it saves.

**7.2 Schema tree.** Schemas, tables, views, columns with types. Row estimates
next to table names. A table's foreign keys shown on its node. Lazy-expanded,
cached, with a refresh control.

**7.3 Schema search.** The tree gets long. A fuzzy finder over every schema,
table, view and column in the connection, opened with the same shortcut as the
panel's command palette conventions. Selecting a table opens its data; selecting
a column opens the table scrolled to it.

**7.4 Editor.** Syntax highlighting, schema-aware autocomplete fed from the
introspection cache, and statement detection from the parse tree. The statement
under the cursor is subtly outlined. Run the current statement, run the
selection, run everything, each with a keyboard shortcut and a visible button.

**7.5 Results grid.** Virtualised rows. Typed rendering: `NULL` is visibly
different from empty, JSON is collapsible, long text truncates with an expander.
Column widths are draggable and remembered per table. Sort locally within the
fetched page, and offer to re-run sorted when the result is truncated. Copy a
cell, a row, or the whole result as CSV, JSON, Markdown or `INSERT` statements.

**7.6 Parameters.** A statement containing `:name` placeholders shows a small
form above the editor. Values are sent separately and bound by the driver, never
interpolated into the string. This is both a convenience and the correct answer
to injection inside saved queries.

**7.7 Table viewer.** Open a table and get its rows with paging, a filter
builder and a sort. The generated SQL is shown and can be edited, which turns
the filter into a lesson rather than a black box.

**7.8 Foreign-key navigation.** In a result or a table view, a value in a
foreign-key column is a link: follow it to the referenced row. On any row, a
control lists tables that reference it, with counts, and opens them filtered.
This is the feature that makes it feel modern and it is nearly free once 5.3 is
right.

**7.9 Query history.** Every statement, with when, how long, how many rows, which
connection, who ran it, and whether it failed. Searchable. One click puts it back
in the editor.

**7.10 Saved queries.** Name a query, optionally bind it to a connection, with
parameters. They belong to the server, not the browser, so they survive a
different machine.

**7.11 Cancel.** A visible cancel that actually cancels server-side.

### The features that make it worth building

**7.12 `EXPLAIN` that is readable.** Not built as an interface: `EXPLAIN` is
SQL and runs like any other statement. The original plan was a button that runs
`EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` and renders the plan as a tree: each
node with its estimated and actual rows, its time, and the worst node
highlighted. Flag the classics, a sequential scan on a large table, an estimate
off by more than an order of magnitude, a spill to disk. Almost nothing
self-hosted does this well, and on a server panel it is the single most useful
thing here. Run the plain `EXPLAIN` by default and require a click for
`ANALYZE`, which executes the statement.

**7.13 Transaction mode.** A toggle that wraps a run in a transaction and leaves
it open, with Commit and Rollback in the status bar and a visible reminder that
one is open. Auto-rollback on session timeout. This is what makes someone brave
enough to run an `UPDATE` on a real database, and it is a small amount of code
for a large amount of confidence.

**7.14 Environment marking.** Built, then removed. Three tiers where only one
changed any behaviour read as a taxonomy rather than a feature, and it put a
control in the header of every connection to say something about one of them.
What survives is per-statement: an unfiltered `UPDATE` or `DELETE`, a `DROP`, a
`TRUNCATE`, an `ALTER` and a `GRANT` ask on every connection, because those are
dangerous on their own terms rather than because of a label. Read-only marking
stays, on the connection, where it refuses writes in the daemon.

**7.15 Inline editing.** When a result maps cleanly to one table with a primary
key, a cell becomes editable. Editing composes the `UPDATE`, shows it, and asks
before running. Same for inserting a row and deleting one. This gives the part of
NocoDB people actually wanted without any of its weight. Refuse politely when
the result is a join or has no key, and say why.

**7.16 Slow query loop.** `internal/db` already reads `pg_stat_statements` and
the Databases page already lists slow queries. Make each one open here, in the
editor, with `EXPLAIN` ready. Find a slow query, understand it, fix it, without
leaving the panel. No standalone client can do this because it does not know
which server it is on.

**7.17 Index suggestions.** When a plan shows a sequential scan over a large
table with a selective filter, offer the `CREATE INDEX` that would help, as text
to copy or run. Suggest, never apply automatically, and always show the
statement.

**7.18 Chart this result.** One chart, not a dashboard. Pick a column for each
axis, choose bar or line, and see it. Export as an image. Resist every request to
make it more than this.

**7.19 Schema snapshots.** Record the schema after DDL runs and show what changed
between two points in time, as a readable diff. This fits Islet's existing story
about being able to see what happened to your server.

**7.20 Scratch restores.** Islet already takes database dumps. Offer to restore a
dump into a temporary database and open it here, so a destructive experiment
happens somewhere safe. Clean up the scratch database on a timer.

**7.21 Keyboard first.** Run, run all, format, new tab, close tab, schema search,
switch connection. All from the keyboard, all discoverable through the existing
command palette. A power user should be able to work without touching the mouse.

---

## 8. Safety

Not optional. This is a tool that executes arbitrary SQL as a privileged user on
someone's production database.

**8.1 Limits.** Default statement timeout 30 seconds, configurable per run to a
hard maximum of 10 minutes. Default row cap 1,000, raisable to 50,000, never
unbounded. A `SELECT *` on a large table must be boring, not fatal.

**8.2 Dangerous statement detection.** Classify each statement before running.
`UPDATE` or `DELETE` with no `WHERE`, `DROP`, `TRUNCATE`, `ALTER`, and anything
else that writes on a connection marked production, each require an explicit
confirmation that names what will happen. Use the panel's own dialog, with the
typed-word confirmation for the worst of them. Never a browser dialog.

**8.3 Read-only connections.** A connection can be marked read-only, which
refuses anything that is not a read, in the daemon and not merely in the
interface.

**8.4 Authorisation.** Admin only in version one. Deployers and viewers do not
get arbitrary SQL: it is equivalent to root on the data. If that changes later,
it must go through project scopes, and the connection list must be filtered by
them, not just the UI.

**8.5 Audit.** Every executed statement goes to the audit log with the actor.
Statements also go to the command log, and the existing `cmdrun.Redact` applies:
no password ever reaches either.

**8.6 Secrets.** Connection passwords are encrypted at rest with `auth.Keys` and
never sent to the browser, not even masked. A test-connection call uses the
stored secret server-side.

**8.7 Parameters are bound, never interpolated.** Saved queries with parameters
must use driver-level binding.

---

## 9. Performance budget

The reason this exists instead of NocoDB. Treat these as acceptance criteria.

| Measure | Budget |
|---|---|
| Idle memory with no session open | 0 above the daemon's baseline |
| Memory per open session | under 10 MB, including one capped result |
| Binary growth from the two drivers | under 5 MB |
| Frontend chunk, lazy-loaded | under 250 kB gzipped, and it must not enter the first chunk |
| Schema tree for 500 tables | under 1 second, cached after |
| First row on screen for a fast query | under 200 ms on the same host |

If the grid needs a dependency to hit its budget, measure and justify it in
`docs/DECISIONS.md` before adding it.

---

## 10. Testing

The repository gates releases on tests, so these are part of the work, not after
it.

**Unit, in Go.**
- The statement splitter, against: semicolons inside single and double quotes,
  `$$ ... $$` and `$tag$ ... $tag$`, `--` and `/* nested /* */ */` comments,
  a `CREATE FUNCTION` body containing semicolons, a trailing statement with no
  semicolon, and an empty document.
- The dangerous-statement classifier, including `UPDATE ... WHERE` inside a
  comment and a `WHERE` that belongs to a subquery.
- Type mapping for every representation decided in 5.4.

**Unit, in TypeScript.** Statement ranges from the parse tree agree with the Go
splitter for the same corpus. Keep one shared fixture file so they cannot drift.

**Integration, against real containers.** Postgres and MySQL started by the
existing test helpers: introspection returns the expected shape, foreign keys
resolve in both directions, the row cap truncates, a timeout fires, and a cancel
actually stops a `pg_sleep`.

**Safety.** A read-only connection refuses a write. A non-admin gets 403 on
every endpoint. No password appears in the command log after a run. Extend
`hack/e2e-privileges.py`, which already proves this class of thing.

**Layout.** Add `/sql` to `hack/layout-audit.mjs` at 1280 and 1440.

---

## 11. Delivery

Ship in order. Each phase should be releasable.

1. **Foundation.** Drivers, connection and pool handling, sessions, the Go
   statement splitter, migrations, the endpoint skeleton, authorisation, audit.
   No interface yet. Tests for the splitter and limits.
2. **Read.** Schema tree, table viewer, results grid, the editor with statement
   detection and run. This is the point where it is already better than Adminer.
3. **Navigate.** Foreign keys in both directions, schema search, history, saved
   queries, parameters.
4. **Write safely.** Transaction mode, dangerous-statement confirmation,
   read-only and production marking, inline editing.
5. **Understand.** `EXPLAIN` rendering, the slow-query loop, index suggestions.
6. **Extras.** Charting, schema snapshots, scratch restores.

Phases 1 to 3 are the product. Everything after is what makes people keep it.

---

## 12. The open questions, answered

1. **MySQL parity.** Both engines ship in version one. Introspection, value
   representation, cancellation and `EXPLAIN` all work on MySQL 8 and MariaDB;
   `hack/e2e-sql.py` runs against Postgres and the same paths are exercised
   against MySQL by hand. The one real difference left is that MySQL's
   `EXPLAIN ANALYZE` is text only, which the plan view says rather than hides.
2. **Where the page lives.** `/sql`, with no sidebar entry. Reached from the
   database you want to look at, on the Databases page. A sidebar entry made it
   read as a third thing beside Databases and Apps, and it is not one: it is
   what you do to a database. External connections are listed on the Databases
   page too, so nothing is reachable only by URL.
3. **External connections and the firewall.** No allow-list. An outbound
   connection from the daemon to a database somebody explicitly added is the
   feature working, not a hole; there is no inbound surface to open and nothing
   for the security report to say that the connection list does not already
   say. Revisit if connections ever stop being admin-only.
4. **Result export size.** Capped by the row cap, deliberately. Copy gives you
   CSV, JSON, Markdown or `INSERT` statements for what is on screen. A real
   export of a million rows is a different feature that streams to a file, and
   it is not this one.
5. **The grid dependency.** None. The grid is about 300 lines in
   `components/sql/ResultsGrid.tsx`, windowed by hand over a fixed row height,
   which is all a capped result needs. The measurement that decided it: the
   whole page is 30 kB gzipped, and the lightest grid worth having was more
   than that on its own.

---

## 13. What is not built

Named so that the next person does not have to work it out from the absence.

- **7.6 parameters.** The backend binds `:name` placeholders through the driver
  and saved queries record which ones they declare. The form above the editor
  that collects values is not built; a saved query with parameters shows them
  and is run by editing the text.
- **7.12 the `EXPLAIN` view.** The endpoint is there and is tested; the plan
  tree and the two buttons are not. `EXPLAIN` typed by hand returns rows like
  any other statement.
- **7.13 the transaction toggle.** There is no checkbox. A query tab holds its
  own connection, so `BEGIN` typed by hand stays open between runs and the
  status bar offers Commit and Roll back when one is.
- **7.15 inline editing.** A cell is not editable. This is the largest piece of
  the specification that is missing, and the one most likely to be asked for.
- **7.18 charting**, **7.19 schema snapshots**, **7.20 scratch restores.** None
  built. Each is a feature in its own right rather than a detail of this one.
- **Integration tests against MySQL in CI.** `hack/e2e-sql.py` covers Postgres.
  MySQL is verified by hand.
