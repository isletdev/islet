-- Backup destinations, plans and runs (restic underneath).
CREATE TABLE backup_destinations (
  id          TEXT PRIMARY KEY,
  server_id   TEXT NOT NULL REFERENCES servers(id),
  name        TEXT NOT NULL UNIQUE,
  type        TEXT NOT NULL,                 -- s3 | sftp | local | rest
  config      BLOB NOT NULL,                 -- encrypted JSON
  password    BLOB NOT NULL,                 -- encrypted restic repository password
  last_check  TEXT NOT NULL DEFAULT '',
  check_ok    INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE backup_plans (
  id             TEXT PRIMARY KEY,
  server_id      TEXT NOT NULL REFERENCES servers(id),
  name           TEXT NOT NULL UNIQUE,
  destination_id TEXT NOT NULL REFERENCES backup_destinations(id),
  sources        TEXT NOT NULL,              -- JSON array of {type, value}
  schedule       TEXT NOT NULL DEFAULT '0 3 * * *',
  keep_daily     INTEGER NOT NULL DEFAULT 7,
  keep_weekly    INTEGER NOT NULL DEFAULT 4,
  keep_monthly   INTEGER NOT NULL DEFAULT 6,
  keep_yearly    INTEGER NOT NULL DEFAULT 1,
  enabled        INTEGER NOT NULL DEFAULT 1,
  next_run_at    TEXT NOT NULL DEFAULT '',
  last_run_at    TEXT NOT NULL DEFAULT '',
  last_status    TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE backup_runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  plan_id     TEXT NOT NULL REFERENCES backup_plans(id) ON DELETE CASCADE,
  trigger     TEXT NOT NULL,                 -- schedule | manual
  status      TEXT NOT NULL,                 -- running | success | failed
  snapshot    TEXT NOT NULL DEFAULT '',
  files_new   INTEGER NOT NULL DEFAULT 0,
  files_changed INTEGER NOT NULL DEFAULT 0,
  bytes_added INTEGER NOT NULL DEFAULT 0,
  bytes_total INTEGER NOT NULL DEFAULT 0,
  log         TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  finished_at TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX backup_runs_plan ON backup_runs(plan_id, id DESC);
