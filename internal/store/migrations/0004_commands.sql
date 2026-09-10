-- Every external command the daemon runs, for the command transparency drawer.

CREATE TABLE commands (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id   TEXT NOT NULL REFERENCES servers (id),
    actor       TEXT NOT NULL,
    command     TEXT NOT NULL,
    exit_code   INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    stderr      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX commands_server_created ON commands (server_id, created_at);
