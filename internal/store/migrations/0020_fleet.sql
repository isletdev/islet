-- Servers this panel manages besides the one it runs on.
--
-- Every managed server runs the same daemon with its own database, so it keeps
-- working when this one is gone: cron, backups, uptime checks and deploys do
-- not stop because the panel's machine rebooted. This table is only the
-- controller's address book and the credentials it needs to reach them.
CREATE TABLE IF NOT EXISTS fleet_servers (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL,
    name         TEXT NOT NULL,
    host         TEXT NOT NULL,           -- address to SSH to
    ssh_port     INTEGER NOT NULL DEFAULT 22,
    ssh_user     TEXT NOT NULL DEFAULT 'root',
    panel_port   INTEGER NOT NULL DEFAULT 9443,

    -- Set once the daemon is installed and answering. The token is this
    -- controller's API token on that server, encrypted at rest; the private key
    -- is the controller's own, generated during the join and never shown.
    api_token_enc TEXT NOT NULL DEFAULT '',
    host_key      TEXT NOT NULL DEFAULT '', -- pinned on first connect

    status       TEXT NOT NULL DEFAULT 'pending', -- pending | joining | ready | unreachable | failed
    status_note  TEXT NOT NULL DEFAULT '',
    version      TEXT NOT NULL DEFAULT '',
    hostname     TEXT NOT NULL DEFAULT '',
    last_seen    TEXT NOT NULL DEFAULT '',

    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    created_by   TEXT NOT NULL DEFAULT '',
    UNIQUE (server_id, name)
);

CREATE INDEX IF NOT EXISTS idx_fleet_servers_server ON fleet_servers (server_id);
