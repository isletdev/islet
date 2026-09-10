-- Scoped API tokens for the CLI, CI and scripts.
CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  server_id    TEXT NOT NULL REFERENCES servers(id),
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  scopes       TEXT NOT NULL DEFAULT '*',   -- '*' or comma list: read, deploy, cron, notify, logs, db, containers
  last_used_at TEXT NOT NULL DEFAULT '',
  expires_at   TEXT NOT NULL DEFAULT '',    -- RFC3339 or empty for never
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX api_tokens_user ON api_tokens(user_id);
