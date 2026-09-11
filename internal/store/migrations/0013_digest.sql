-- Notification digests: a channel can batch non-critical events hourly or daily.
ALTER TABLE channels ADD COLUMN digest TEXT NOT NULL DEFAULT '';

-- deliveries gains the 'digest' status; SQLite cannot alter a CHECK, so rebuild.
CREATE TABLE deliveries_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    channel_id  TEXT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'digest')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_try_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    error       TEXT NOT NULL DEFAULT '',
    sent_at     TEXT
);
INSERT INTO deliveries_new (id, event_id, channel_id, status, attempts, next_try_at, error, sent_at)
    SELECT id, event_id, channel_id, status, attempts, next_try_at, error, sent_at FROM deliveries;
DROP TABLE deliveries;
ALTER TABLE deliveries_new RENAME TO deliveries;
CREATE INDEX deliveries_pending ON deliveries (status, next_try_at);
