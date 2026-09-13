"""Exercise the SQL client against a real database.

Everything above the drivers is covered by unit tests against a fake one. This
is the other half: the catalog queries, the type mapping and the row cap have
to meet a server that disagrees with us, and that is the part a fake cannot
check.

It needs a running daemon and a Postgres it can reach. Start one with:

    docker run -d --name islet-sqltest-pg -e POSTGRES_PASSWORD=devpass \\
        -e POSTGRES_DB=shop -p 15433:5432 postgres:17-alpine

seed it with the shape these checks expect:

    docker exec -i islet-sqltest-pg psql -U postgres -d shop <<'SQL'
    CREATE TABLE customers (id bigserial PRIMARY KEY, email text NOT NULL UNIQUE,
      name text NOT NULL, country char(2) NOT NULL DEFAULT 'MK',
      credit numeric(12,2) NOT NULL DEFAULT 0, tags text[], meta jsonb,
      created_at timestamptz NOT NULL DEFAULT now());
    CREATE TABLE orders (id bigserial PRIMARY KEY,
      customer_id bigint NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
      total numeric(12,2) NOT NULL, status text NOT NULL DEFAULT 'new');
    CREATE INDEX orders_customer ON orders (customer_id);
    CREATE TABLE order_lines (id bigserial PRIMARY KEY,
      order_id bigint NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
      sku text NOT NULL, qty int NOT NULL DEFAULT 1, price numeric(10,2) NOT NULL);
    CREATE VIEW big_orders AS SELECT * FROM orders WHERE total > 100;
    INSERT INTO customers (email, name, credit, tags, meta)
    SELECT 'user'||i||'@example.com','Customer '||i,(i*7.25)::numeric,
           ARRAY['vip'],jsonb_build_object('score',i) FROM generate_series(1,500) i;
    INSERT INTO orders (customer_id,total) SELECT 1+(i%500),(i*3.5)::numeric FROM generate_series(1,2000) i;
    INSERT INTO order_lines (order_id,sku,price)
    SELECT 1+(i%2000),'SKU-'||(i%90),9.99 FROM generate_series(1,6000) i;
    ANALYZE;
    SQL

then run:

    python hack/e2e-sql.py "$SESSION_COOKIE" [host] [port]

The first customer's credit is 7.25, which is the one value a check reads by
number rather than by shape.
"""
import http.cookiejar, json, sys, urllib.error, urllib.request

sys.stdout.reconfigure(encoding="utf-8")
BASE = "http://127.0.0.1:9443"
COOKIE = sys.argv[1]
HOST = sys.argv[2] if len(sys.argv) > 2 else "127.0.0.1"
PORT = int(sys.argv[3]) if len(sys.argv) > 3 else 15433

cj = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
cj.set_cookie(http.cookiejar.Cookie(0, "islet_session", COOKIE, None, False, "127.0.0.1",
                                    False, False, "/", True, False, None, False, None, None, {}))
fails = []


def call(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"})
    try:
        with op.open(req, timeout=180) as r:
            return r.status, r.read().decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + ("  " + str(detail)[:160] if detail else ""))
    if not ok:
        fails.append(name)


def query(ref, text, **opts):
    """Run a document and return (statements, summary, error)."""
    body = {"sql": text, "sessionId": "", "runId": "", "rowCap": 0, "timeoutMs": 0,
            "transaction": False, "continueOnError": False, "params": None, "confirmed": False}
    body.update(opts)
    st, raw = call("POST", f"/api/v1/sql/connections/{ref}/query", body)
    if st != 200:
        return [], None, (st, raw)
    stmts, summary = [], None
    for line in raw.splitlines():
        if not line.startswith("data: "):
            continue
        p = json.loads(line[6:])
        if "columns" in p or "rowCount" in p:
            stmts.append(p)
        elif "statements" in p:
            summary = p
    return stmts, summary, None


# ---- a connection, tested before it is saved ------------------------------

st, body = call("POST", "/api/v1/sql/connections", {
    "name": "e2e-nowhere", "engine": "postgres", "host": "127.0.0.1", "port": 1,
    "username": "nobody", "password": "x", "database": "x", "tls": "disable",
    "readOnly": False})
check("a connection that cannot be reached is not saved", st == 400, body[:120])

st, body = call("POST", "/api/v1/sql/connections", {
    "name": "e2e-shop", "engine": "postgres", "host": HOST, "port": PORT,
    "username": "postgres", "password": "devpass", "database": "shop", "tls": "disable",
    "readOnly": False})
if st != 201:
    print("cannot continue without a connection:", st, body[:300])
    sys.exit(1)
ref = json.loads(body)["id"]
check("a reachable connection is saved", True, ref)
check("the create response carries no password", "devpass" not in body)

st, body = call("GET", "/api/v1/sql/connections")
check("the list carries no password", "devpass" not in body)

st, body = call("POST", f"/api/v1/sql/connections/{ref}/test")
check("the connection test reports a version", st == 200 and "PostgreSQL" in body, body[:90])

# ---- the schema ------------------------------------------------------------

st, body = call("GET", f"/api/v1/sql/connections/{ref}/schema")
tree = json.loads(body) if st == 200 else {}
tables = {t["name"]: t for s in tree.get("schemas", []) for t in s["tables"]}
check("introspection finds the tables", {"customers", "orders", "order_lines"} <= set(tables), sorted(tables))
check("a view is reported as a view", tables.get("big_orders", {}).get("kind") == "view",
      tables.get("big_orders", {}).get("kind"))
check("row estimates come from the catalog, not COUNT(*)",
      tables.get("customers", {}).get("rows", -1) > 0, tables.get("customers", {}).get("rows"))
check("foreign keys resolve in the referencing direction",
      any(fk["refTable"] == "customers" for fk in tables.get("orders", {}).get("foreignKeys") or []))
check("foreign keys resolve in the reverse direction",
      any(fk["table"] == "orders" for fk in tables.get("customers", {}).get("referencedBy") or []))

cols = {c["name"]: c for c in tables.get("customers", {}).get("columns") or []}
check("a primary key is marked", cols.get("id", {}).get("primaryKey") is True)
check("nullability is read", cols.get("email", {}).get("nullable") is False)

st, body = call("GET", f"/api/v1/sql/connections/{ref}/table/public/orders")
detail = json.loads(body) if st == 200 else {}
check("a table's indexes are read", any(i["name"] == "orders_customer" for i in detail.get("indexes") or []))
check("a table's primary key constraint is read",
      any(c["type"] == "primary" for c in detail.get("constraints") or []))

st, body = call("GET", f"/api/v1/sql/connections/{ref}/search?q=custom")
matches = json.loads(body) if st == 200 else []
check("schema search finds a table", any(m["kind"] == "table" and m["table"] == "customers" for m in matches))

# ---- values, typed ---------------------------------------------------------

stmts, summary, err = query(ref, """
SELECT id, credit, tags, meta, created_at, NULL::text AS nothing, ''::text AS empty,
       'x'::bytea AS blob, interval '2 days' AS iv, 9007199254740993::int8 AS big,
       'inf'::float8 AS inf, now()::date AS d, now()::time AS t
FROM customers ORDER BY id LIMIT 1
""")
check("a typed select runs", err is None and len(stmts) == 1, err)
if stmts:
    r = stmts[0]
    by = {c["name"]: r["rows"][0][i] for i, c in enumerate(r["columns"])}
    cls = {c["name"]: c["class"] for c in r["columns"]}
    check("a bigint stays exact, as a string", by["big"] == "9007199254740993", by["big"])
    check("a numeric is its own digits, not a struct", by["credit"] == "7.25", by["credit"])
    check("NULL is null and an empty string is not", by["nothing"] is None and by["empty"] == "")
    check("an array is an array", isinstance(by["tags"], list) and "vip" in by["tags"], by["tags"])
    check("jsonb is embedded, not stringified", isinstance(by["meta"], dict), by["meta"])
    check("bytea is base64", by["blob"] == "eA==", by["blob"])
    check("an interval keeps the server's own words", "2 day" in str(by["iv"]), by["iv"])
    check("infinity survives JSON", by["inf"] == "Infinity", by["inf"])
    check("a date has no time glued to it", by["d"].count(":") == 0 and cls["d"] == "date", by["d"])
    check("a time has no day glued to it", "T" not in str(by["t"]) and cls["t"] == "time", by["t"])

# ---- the row cap -----------------------------------------------------------

stmts, summary, err = query(ref, "SELECT * FROM order_lines", rowCap=10)
check("the row cap truncates and says so",
      stmts and stmts[0]["rowCount"] == 10 and stmts[0]["truncated"] is True,
      stmts[0] if stmts else err)

stmts, summary, err = query(ref, "SELECT * FROM order_lines", rowCap=10_000_000)
check("an absurd row cap is clamped rather than honoured",
      stmts and stmts[0]["rowCount"] <= 50_000, stmts[0]["rowCount"] if stmts else err)

# ---- batches ---------------------------------------------------------------

stmts, summary, err = query(ref, "SELECT 1 AS a; SELECT 2 AS b; SELECT 3 AS c")
check("a batch reports one message per statement", len(stmts) == 3, len(stmts))

stmts, summary, err = query(ref, "SELECT 1 AS ok; SELECT * FROM does_not_exist; SELECT 2 AS never")
check("a failure stops the batch", summary and summary["statements"] == 2 and summary["failed"] == 1, summary)
check("the failure says what went wrong",
      len(stmts) > 1 and stmts[1].get("error", {}).get("message", ""), stmts[1].get("error") if len(stmts) > 1 else "")

stmts, summary, err = query(ref, "SELECT 1 AS ok; SELECT * FROM does_not_exist; SELECT 2 AS after",
                            continueOnError=True)
check("continuing past an error runs the rest", summary and summary["statements"] == 3, summary)

# ---- timeouts and statements that need asking -------------------------------

stmts, summary, err = query(ref, "SELECT pg_sleep(5)", timeoutMs=1000)
check("a statement past its timeout is stopped",
      stmts and stmts[0].get("error", {}).get("code") == "timeout", stmts[0].get("error") if stmts else err)

_, _, err = query(ref, "DELETE FROM order_lines")
check("an unfiltered delete is refused until it is confirmed",
      err and err[0] == 409 and "needs_confirmation" in err[1], err)

_, _, err = query(ref, "DROP TABLE IF EXISTS e2e_nothing")
check("a drop is refused until it is confirmed", err and err[0] == 409, err)

# ---- DDL, and the cache that has to notice -----------------------------------

stmts, summary, err = query(ref, "CREATE TABLE e2e_scratch (id int primary key, note text)", confirmed=True)
check("DDL runs when confirmed", err is None and summary and summary["failed"] == 0, err or summary)
check("DDL says the schema changed", summary and summary["schemaChanged"] is True, summary)

st, body = call("GET", f"/api/v1/sql/connections/{ref}/schema")
check("the schema cache was invalidated by the DDL", "e2e_scratch" in body)

stmts, summary, err = query(ref, "INSERT INTO e2e_scratch (id, note) VALUES (1, 'one'), (2, 'two')")
check("an insert reports the rows it affected",
      stmts and stmts[0]["rowsAffected"] == 2, stmts[0] if stmts else err)

stmts, summary, err = query(ref, "UPDATE e2e_scratch SET note = 'x' WHERE id = 1")
check("a filtered update needs no confirmation", err is None and summary and summary["failed"] == 0, err)

stmts, summary, err = query(ref, "DROP TABLE e2e_scratch", confirmed=True)
check("the scratch table is removed", err is None and summary and summary["failed"] == 0, err)

# ---- read-only, in the daemon ------------------------------------------------

st, body = call("POST", "/api/v1/sql/connections", {
    "name": "e2e-readonly", "engine": "postgres", "host": HOST, "port": PORT,
    "username": "postgres", "password": "devpass", "database": "shop", "tls": "disable",
    "readOnly": True})
ro = json.loads(body)["id"] if st == 201 else None
check("a read-only connection can be created", ro is not None, body[:120])
if ro:
    _, _, err = query(ro, "SELECT 1")
    check("a read-only connection still reads", err is None, err)
    _, _, err = query(ro, "UPDATE orders SET status = 'x' WHERE id = 1", confirmed=True)
    check("a read-only connection refuses a write even when confirmed",
          err and err[0] == 403 and "read_only" in err[1], err)
    call("DELETE", f"/api/v1/sql/connections/{ro}")

# ---- explain ------------------------------------------------------------------

st, body = call("POST", f"/api/v1/sql/connections/{ref}/explain",
                {"sql": "SELECT * FROM order_lines WHERE sku = 'SKU-7'", "sessionId": "", "analyze": False})
plan = json.loads(body) if st == 200 else {}
check("EXPLAIN returns a plan as JSON, not as Go's map syntax",
      isinstance(plan.get("json"), (list, dict)) and not plan.get("text"), str(plan)[:120])
check("EXPLAIN without analyze did not run the statement", plan.get("analyzed") is False)

st, body = call("POST", f"/api/v1/sql/connections/{ref}/explain",
                {"sql": "DELETE FROM order_lines", "sessionId": "", "analyze": True})
check("EXPLAIN ANALYZE goes through the same preflight as a run", st == 409, (st, body[:120]))

# ---- sessions -----------------------------------------------------------------

st, body = call("POST", "/api/v1/sql/sessions", {"ref": ref})
sess = json.loads(body)["id"] if st == 201 else None
check("a session opens", sess is not None, body[:120])
if sess:
    stmts, summary, err = query(ref, "BEGIN", sessionId=sess)
    check("a transaction can be opened on a session", summary and summary["inTransaction"] is True, summary)
    stmts, summary, err = query(ref, "ROLLBACK", sessionId=sess)
    check("and closed again", summary and summary["inTransaction"] is False, summary)
    st, _ = call("DELETE", f"/api/v1/sql/sessions/{sess}")
    check("a session closes", st == 204, st)

# ---- history, and what must not be in it ---------------------------------------

st, body = call("GET", f"/api/v1/sql/history?ref={ref}&limit=50")
entries = json.loads(body) if st == 200 else []
check("every statement reached the history", len(entries) >= 10, len(entries))
check("a failure is recorded with its reason", any(e.get("error") for e in entries))

st, body = call("GET", "/api/v1/commands?limit=300")
check("no connection password reached the command log", "devpass" not in body)

# ---- saved queries ---------------------------------------------------------------

st, body = call("POST", "/api/v1/sql/saved", {
    "name": "e2e customers by country", "description": "",
    "sql": "SELECT country, count(*) FROM customers WHERE country = :country GROUP BY country",
    "connectionRef": ref, "engine": "postgres"})
saved = json.loads(body) if st == 201 else {}
check("a saved query keeps its parameters", saved.get("params") == ["country"], saved.get("params"))
if saved.get("id"):
    st, _ = call("DELETE", f"/api/v1/sql/saved/{saved['id']}")
    check("a saved query can be deleted", st == 204, st)

call("DELETE", f"/api/v1/sql/connections/{ref}")

print()
print("FAILURES:", fails if fails else "none")
sys.exit(1 if fails else 0)
