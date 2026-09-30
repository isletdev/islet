-- islet:no-transaction
--
-- Two more places bytes can live: Google Cloud Storage and Azure Blob Storage.
--
-- The driver column is a CHECK constraint and SQLite cannot alter one, so
-- widening it is a table rebuild. `media_objects` points at this table, and a
-- parent with live children cannot be dropped while foreign keys are on — which
-- is why this migration runs outside the usual transaction, on a connection
-- with the constraints switched off. The runner puts them back and then proves
-- nothing is dangling before it records the migration.
--
-- Rehearsed against a copy of a real database rather than an empty one, which
-- is the only reason any of the above is here: on a fresh install there are no
-- objects, the drop succeeds, and this would have shipped and failed on exactly
-- the servers that had been using the feature. Two earlier attempts —
-- `PRAGMA defer_foreign_keys` and `PRAGMA legacy_alter_table` — both looked
-- right and both failed the same way, because dropping a parent counts a
-- violation per child row and recreating the parent under the same name does
-- not take it back.
--
-- Why these two drivers and not more: the S3 driver already covers AWS, R2,
-- MinIO, Backblaze, Wasabi and Hetzner with an endpoint, and Google's buckets
-- answer the S3 API too if you make an HMAC key for them. Azure speaks nothing
-- S3-shaped at all, and Google's own credential is a service account rather
-- than an HMAC pair. Those are the two gaps a driver actually closes.
DROP TABLE IF EXISTS media_buckets_new;

CREATE TABLE media_buckets_new (
    id            TEXT PRIMARY KEY,
    server_id     TEXT NOT NULL REFERENCES servers (id),
    name          TEXT NOT NULL,
    driver        TEXT NOT NULL CHECK (driver IN ('local', 's3', 'gcs', 'azure')),
    config        TEXT NOT NULL DEFAULT '{}',
    secret_key    TEXT NOT NULL DEFAULT '',
    access_key    TEXT NOT NULL DEFAULT '',
    public_base   TEXT NOT NULL DEFAULT '',
    max_bytes     INTEGER NOT NULL DEFAULT 0,
    allow_types   TEXT NOT NULL DEFAULT '',
    scan_uploads  INTEGER NOT NULL DEFAULT 1 CHECK (scan_uploads IN (0, 1)),
    is_default    INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO media_buckets_new
    (id, server_id, name, driver, config, secret_key, access_key, public_base,
     max_bytes, allow_types, scan_uploads, is_default, created_at, updated_at)
SELECT id, server_id, name, driver, config, secret_key, access_key, public_base,
     max_bytes, allow_types, scan_uploads, is_default, created_at, updated_at
FROM media_buckets;

DROP TABLE media_buckets;
ALTER TABLE media_buckets_new RENAME TO media_buckets;
CREATE UNIQUE INDEX media_buckets_name ON media_buckets (server_id, name);
