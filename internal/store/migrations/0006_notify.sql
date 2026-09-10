-- Notification channels, events, and the delivery outbox.

CREATE TABLE channels (
    id            TEXT PRIMARY KEY,
    server_id     TEXT NOT NULL REFERENCES servers (id),
    type          TEXT NOT NULL CHECK (type IN ('telegram', 'discord', 'slack', 'email', 'ntfy', 'gotify', 'pushover', 'webhook')),
    name          TEXT NOT NULL,
    config_enc    BLOB NOT NULL,                  -- AES-GCM encrypted JSON with tokens and addresses
    categories    TEXT NOT NULL DEFAULT '*',      -- comma list of event categories or *
    min_severity  TEXT NOT NULL DEFAULT 'warning' CHECK (min_severity IN ('info', 'warning', 'critical')),
    quiet_from    TEXT NOT NULL DEFAULT '',       -- HH:MM local, empty = none; critical always goes through
    quiet_to      TEXT NOT NULL DEFAULT '',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id   TEXT NOT NULL REFERENCES servers (id),
    category    TEXT NOT NULL,                    -- system, security, deploy, container, database, domain, cron, backup, runner, uptime, custom
    severity    TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    title       TEXT NOT NULL,
    message     TEXT NOT NULL DEFAULT '',
    link        TEXT NOT NULL DEFAULT '',         -- panel path, e.g. /containers/abc
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX events_server_created ON events (server_id, created_at);

CREATE TABLE deliveries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    channel_id  TEXT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_try_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    error       TEXT NOT NULL DEFAULT '',
    sent_at     TEXT
);

CREATE INDEX deliveries_pending ON deliveries (status, next_try_at);
