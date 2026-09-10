-- Uptime checks run from this server.
CREATE TABLE checks (
  id            TEXT PRIMARY KEY,
  server_id     TEXT NOT NULL REFERENCES servers(id),
  name          TEXT NOT NULL,
  type          TEXT NOT NULL,                 -- http | tcp | keyword
  target        TEXT NOT NULL,                 -- URL or host:port
  keyword       TEXT NOT NULL DEFAULT '',
  interval_sec  INTEGER NOT NULL DEFAULT 60,
  timeout_sec   INTEGER NOT NULL DEFAULT 10,
  expect_status INTEGER NOT NULL DEFAULT 0,    -- 0 means any 2xx/3xx
  enabled       INTEGER NOT NULL DEFAULT 1,
  status        TEXT NOT NULL DEFAULT 'unknown',
  failures      INTEGER NOT NULL DEFAULT 0,
  last_check_at TEXT NOT NULL DEFAULT '',
  last_latency  INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT NOT NULL DEFAULT '',
  down_since    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX checks_server ON checks(server_id);

CREATE TABLE check_results (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  check_id   TEXT NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  ok         INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX check_results_check ON check_results(check_id, id DESC);
