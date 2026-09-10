-- Islet schema, migration 0001.
-- Convention: every table that describes something running on a machine
-- carries server_id, always the local server for now (see docs/DECISIONS.md).

CREATE TABLE servers (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    hostname   TEXT NOT NULL DEFAULT '',
    is_local   INTEGER NOT NULL DEFAULT 0 CHECK (is_local IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX servers_one_local ON servers (is_local) WHERE is_local = 1;

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id  TEXT NOT NULL REFERENCES servers (id),
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX audit_log_server_created ON audit_log (server_id, created_at);
