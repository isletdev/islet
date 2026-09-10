-- Host metrics samples. One row every 10 seconds, kept for 7 days
-- (about 60k rows). Aggregation happens at query time.

CREATE TABLE metrics_samples (
    server_id   TEXT NOT NULL REFERENCES servers (id),
    ts          INTEGER NOT NULL,            -- unix seconds
    cpu_pct     REAL NOT NULL,               -- 0..100 across all cores
    load1       REAL NOT NULL DEFAULT 0,
    load5       REAL NOT NULL DEFAULT 0,
    load15      REAL NOT NULL DEFAULT 0,
    mem_used    INTEGER NOT NULL,            -- bytes
    mem_total   INTEGER NOT NULL,
    swap_used   INTEGER NOT NULL DEFAULT 0,
    swap_total  INTEGER NOT NULL DEFAULT 0,
    disk_used   INTEGER NOT NULL,            -- root filesystem, bytes
    disk_total  INTEGER NOT NULL,
    net_rx      INTEGER NOT NULL DEFAULT 0,  -- bytes/s, all interfaces except loopback
    net_tx      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, ts)
) WITHOUT ROWID;
