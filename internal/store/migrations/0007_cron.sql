-- Cron jobs, their runs and script history.
CREATE TABLE jobs (
  id          TEXT PRIMARY KEY,
  server_id   TEXT NOT NULL REFERENCES servers(id),
  name        TEXT NOT NULL,
  type        TEXT NOT NULL,                  -- command | script | file | container | image | http | chain | heartbeat
  schedule    TEXT NOT NULL DEFAULT '',       -- cron expression; empty means manual only
  timezone    TEXT NOT NULL DEFAULT '',       -- IANA name; empty means server local time
  command     TEXT NOT NULL DEFAULT '',       -- command, file path, image, URL, job ids (chain) or ping token (heartbeat)
  script      TEXT NOT NULL DEFAULT '',       -- inline script body (type script) or command inside container/image
  container   TEXT NOT NULL DEFAULT '',
  http_method TEXT NOT NULL DEFAULT 'GET',
  work_dir    TEXT NOT NULL DEFAULT '',
  run_as      TEXT NOT NULL DEFAULT '',
  timeout_sec INTEGER NOT NULL DEFAULT 3600,
  overlap     TEXT NOT NULL DEFAULT 'skip',   -- skip | queue | kill
  retries     INTEGER NOT NULL DEFAULT 0,
  nice        INTEGER NOT NULL DEFAULT 0,
  jitter_sec  INTEGER NOT NULL DEFAULT 0,
  grace_sec   INTEGER NOT NULL DEFAULT 0,     -- heartbeat: how late a ping may be before alerting
  notify_on   TEXT NOT NULL DEFAULT 'failure',-- failure | always | never
  enabled     INTEGER NOT NULL DEFAULT 1,
  last_ping_at TEXT NOT NULL DEFAULT '',
  overdue     INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX jobs_server ON jobs(server_id);

CREATE TABLE job_runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id      TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  trigger     TEXT NOT NULL,                  -- schedule | manual | chain | retry
  attempt     INTEGER NOT NULL DEFAULT 1,
  status      TEXT NOT NULL,                  -- running | success | failed | timeout | killed
  exit_code   INTEGER NOT NULL DEFAULT 0,
  output      TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  finished_at TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX job_runs_job ON job_runs(job_id, id DESC);

CREATE TABLE script_versions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id     TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  actor      TEXT NOT NULL DEFAULT '',
  content    TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX script_versions_job ON script_versions(job_id, id DESC);
