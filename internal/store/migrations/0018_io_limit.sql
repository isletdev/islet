-- Per-app disk IO limit (MB/s, 0 = unlimited) applied with --device-read/write-bps.
ALTER TABLE apps ADD COLUMN io_mbps INTEGER NOT NULL DEFAULT 0;
