-- Self-hosted CI runners.
CREATE TABLE runner_pools (
  id             TEXT PRIMARY KEY,
  server_id      TEXT NOT NULL REFERENCES servers(id),
  provider       TEXT NOT NULL,                 -- github | gitlab | gitea
  name           TEXT NOT NULL UNIQUE,
  url            TEXT NOT NULL,                 -- repo/org URL (github), instance URL (gitlab, gitea)
  token          BLOB NOT NULL DEFAULT '',      -- encrypted PAT (github) or registration token
  labels         TEXT NOT NULL DEFAULT '',
  min_idle       INTEGER NOT NULL DEFAULT 1,
  max_runners    INTEGER NOT NULL DEFAULT 2,
  docker_access  INTEGER NOT NULL DEFAULT 0,    -- mount the Docker socket into jobs
  memory_mb      INTEGER NOT NULL DEFAULT 0,
  cpus           REAL NOT NULL DEFAULT 0,
  webhook_secret TEXT NOT NULL DEFAULT '',
  enabled        INTEGER NOT NULL DEFAULT 1,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE runner_jobs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  pool_id     TEXT NOT NULL REFERENCES runner_pools(id) ON DELETE CASCADE,
  external_id TEXT NOT NULL DEFAULT '',        -- workflow job id from the webhook
  name        TEXT NOT NULL DEFAULT '',
  repo        TEXT NOT NULL DEFAULT '',
  runner      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,                   -- queued | in_progress | completed
  conclusion  TEXT NOT NULL DEFAULT '',
  url         TEXT NOT NULL DEFAULT '',
  queued_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  started_at  TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runner_jobs_pool ON runner_jobs(pool_id, id DESC);
