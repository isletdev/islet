-- The SQL client: saved external connections, saved queries, and the history
-- of every statement actually executed.
--
-- Databases Islet installed need no row in sql_connections. They are listed
-- from the database service at request time and referenced by instance name,
-- so the common case has nothing to configure and nothing to keep in step.
CREATE TABLE sql_connections (
  id           TEXT PRIMARY KEY,
  server_id    TEXT NOT NULL REFERENCES servers(id),
  name         TEXT NOT NULL,
  engine       TEXT NOT NULL,                      -- postgres | mysql
  host         TEXT NOT NULL,
  port         INTEGER NOT NULL,
  username     TEXT NOT NULL,
  database     TEXT NOT NULL DEFAULT '',
  password_enc BLOB NOT NULL DEFAULT '',           -- encrypted; never returned to the client
  tls_mode     TEXT NOT NULL DEFAULT 'disable',    -- disable | require | verify-full
  read_only    INTEGER NOT NULL DEFAULT 0,         -- enforced in the daemon, not the interface
  environment  TEXT NOT NULL DEFAULT 'development',-- development | staging | production
  created_by   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE (server_id, name)
);

CREATE TABLE sql_saved_queries (
  id             TEXT PRIMARY KEY,
  server_id      TEXT NOT NULL REFERENCES servers(id),
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  sql            TEXT NOT NULL,
  connection_ref TEXT NOT NULL DEFAULT '',         -- instance name or sql_connections.id, may be empty
  params_json    TEXT NOT NULL DEFAULT '[]',       -- declared :name parameters
  created_by     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE (server_id, name)
);

-- Every statement actually executed. Most clients do this badly and it is
-- nearly free here. It is pruned; a history table that grows forever is a bug.
CREATE TABLE sql_history (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id      TEXT NOT NULL REFERENCES servers(id),
  connection_ref TEXT NOT NULL,
  sql            TEXT NOT NULL,
  actor          TEXT NOT NULL,
  kind           TEXT NOT NULL DEFAULT '',         -- read | write | ddl | ...
  started_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  row_count      INTEGER NOT NULL DEFAULT 0,       -- rows returned or affected
  truncated      INTEGER NOT NULL DEFAULT 0,
  error          TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_sql_history_recent ON sql_history (server_id, started_at DESC);
CREATE INDEX idx_sql_history_conn ON sql_history (server_id, connection_ref, started_at DESC);

-- Read-only and environment for a connection that has no row of its own.
-- Databases Islet installed are listed from the database service at request
-- time, so this is the only place a mark on one can live, and marking the
-- production database is the case the feature exists for.
CREATE TABLE sql_marks (
  server_id   TEXT NOT NULL REFERENCES servers(id),
  ref         TEXT NOT NULL,                       -- islet:<instance>
  read_only   INTEGER NOT NULL DEFAULT 0,
  environment TEXT NOT NULL DEFAULT 'development', -- development | staging | production
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  PRIMARY KEY (server_id, ref)
);
