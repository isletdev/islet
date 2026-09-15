-- A place to put a secret once and refer to it everywhere else.
--
-- Islet already encrypts credentials in half a dozen places — an app's
-- environment, a registry login, a DNS provider token, the assistant's API key
-- — each in its own setting, each reachable only from the page that wrote it.
-- The same database password ends up pasted into an app, a cron job and a
-- backup hook, and rotating it means remembering all three.
--
-- A value is stored encrypted with the daemon's key, exactly as those others
-- are; what this adds is a name to refer to it by. Nothing here stores the
-- value in the clear, and nothing reads it back except a deliberate, audited
-- reveal or the substitution that happens on the way into a process.
--
-- server_id is on it because every machine-bound table carries one, so a fleet
-- never needs a schema rewrite.
CREATE TABLE vault_secrets (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    value_enc   BLOB NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at TEXT,
    UNIQUE (server_id, name)
);

CREATE INDEX vault_secrets_server ON vault_secrets (server_id, name);
