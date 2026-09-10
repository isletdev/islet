-- Deployed apps and their releases.
CREATE TABLE apps (
  id             TEXT PRIMARY KEY,
  server_id      TEXT NOT NULL REFERENCES servers(id),
  name           TEXT NOT NULL UNIQUE,
  source         TEXT NOT NULL,                  -- git | image
  repo_url       BLOB NOT NULL DEFAULT '',       -- encrypted; may carry a token
  branch         TEXT NOT NULL DEFAULT 'main',
  root_dir       TEXT NOT NULL DEFAULT '',
  image          TEXT NOT NULL DEFAULT '',       -- source=image
  strategy       TEXT NOT NULL DEFAULT 'auto',   -- auto | static | node | python | go | dockerfile | compose | image
  framework      TEXT NOT NULL DEFAULT '',
  install_cmd    TEXT NOT NULL DEFAULT '',
  build_cmd      TEXT NOT NULL DEFAULT '',
  start_cmd      TEXT NOT NULL DEFAULT '',
  output_dir     TEXT NOT NULL DEFAULT '',
  port           INTEGER NOT NULL DEFAULT 0,
  health_path    TEXT NOT NULL DEFAULT '/',
  predeploy_cmd  TEXT NOT NULL DEFAULT '',
  env            BLOB NOT NULL DEFAULT '',       -- encrypted KEY=VALUE lines
  domain         TEXT NOT NULL DEFAULT '',
  tls            TEXT NOT NULL DEFAULT 'letsencrypt',
  webhook_secret TEXT NOT NULL DEFAULT '',
  auto_deploy    INTEGER NOT NULL DEFAULT 1,
  memory_mb      INTEGER NOT NULL DEFAULT 0,
  cpus           REAL NOT NULL DEFAULT 0,
  volumes        TEXT NOT NULL DEFAULT '',       -- container paths to persist, one per line
  current_release INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'new',    -- new | building | live | failed | stopped
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX apps_server ON apps(server_id);

CREATE TABLE releases (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  number      INTEGER NOT NULL,
  trigger     TEXT NOT NULL,                     -- manual | push | redeploy | rollback | api
  actor       TEXT NOT NULL DEFAULT '',
  commit_sha  TEXT NOT NULL DEFAULT '',
  message     TEXT NOT NULL DEFAULT '',
  author      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,                     -- queued | building | deploying | live | superseded | failed | cancelled
  image       TEXT NOT NULL DEFAULT '',
  container   TEXT NOT NULL DEFAULT '',
  log         TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  finished_at TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX releases_app ON releases(app_id, id DESC);
