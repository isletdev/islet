-- The first service an application calls rather than an operator.
--
-- Everything in this database so far describes what the operator did to their
-- server. These four tables describe what their applications are doing with it:
-- a bucket somebody's photographs live in, a key their app holds, the objects
-- it has uploaded, and the sizes it wants them in.
--
-- That difference is why the key table is not `api_tokens` and never will be.
-- An API token carries the operator's authority narrowed by scopes; a media key
-- carries no authority at all outside its own namespace, is held by software on
-- the open internet, and is refused by the panel API. Two things that look alike
-- and must never be the same row.

-- Where bytes actually live.
--
-- `local` is this server's disk under the data directory. `s3` is anything that
-- speaks S3 — AWS, Cloudflare R2, MinIO, Backblaze, Wasabi, Hetzner — which is
-- one driver with an endpoint and a path-style toggle rather than five.
CREATE TABLE media_buckets (
    id            TEXT PRIMARY KEY,
    server_id     TEXT NOT NULL REFERENCES servers (id),
    name          TEXT NOT NULL,
    driver        TEXT NOT NULL CHECK (driver IN ('local', 's3')),
    -- Driver configuration as JSON: endpoint, region, bucket, prefix,
    -- path-style. Not columns, because the next driver has different ones and a
    -- column per vendor is how a schema stops being readable.
    config        TEXT NOT NULL DEFAULT '{}',
    -- Sealed with the daemon's key, exactly as every other stored credential.
    secret_key    TEXT NOT NULL DEFAULT '',
    access_key    TEXT NOT NULL DEFAULT '',
    -- Where the public can fetch objects in this bucket, when they are public:
    -- an R2 custom domain, a CDN in front of S3, or empty to have this server
    -- serve them itself.
    public_base   TEXT NOT NULL DEFAULT '',
    -- Refuse anything bigger, in bytes. 0 is the service default.
    max_bytes     INTEGER NOT NULL DEFAULT 0,
    -- Content types this bucket accepts, comma separated, with a trailing /* to
    -- mean a whole family. Empty accepts anything the service accepts.
    allow_types   TEXT NOT NULL DEFAULT '',
    -- Whether uploads here are scanned for malware. On by default for anything
    -- arriving through the daemon; a presigned upload straight to a bucket is
    -- not read back unless this says to.
    scan_uploads  INTEGER NOT NULL DEFAULT 1 CHECK (scan_uploads IN (0, 1)),
    is_default    INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX media_buckets_name ON media_buckets (server_id, name);

-- What an application holds to talk to the service.
--
-- Hashed, never stored in the clear, shown once at creation — the same
-- arrangement as an API token, for the same reason, and with none of the same
-- authority. `namespace` is the wall between two apps on one server: a key sees
-- objects in its namespace and cannot name another one.
CREATE TABLE media_keys (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL REFERENCES servers (id),
    name         TEXT NOT NULL,
    -- The first characters of the key, so the panel can show which one a row is
    -- without being able to reconstruct it.
    prefix       TEXT NOT NULL,
    hash         TEXT NOT NULL,
    namespace    TEXT NOT NULL DEFAULT '',
    bucket_id    TEXT NOT NULL DEFAULT '',
    -- What this key may do: any of upload, read, delete, sign. Comma separated,
    -- because a set of four values does not need a table.
    scopes       TEXT NOT NULL DEFAULT 'upload,read',
    -- Requests a minute, and total bytes this key may store. 0 is the service
    -- default and the service default is not unlimited.
    rate_per_min INTEGER NOT NULL DEFAULT 0,
    quota_bytes  INTEGER NOT NULL DEFAULT 0,
    -- Origins a browser may call from, comma separated. Empty means no browser
    -- may: a key with no origins is a server-side key, and that is the safer
    -- thing to be by default.
    origins      TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL DEFAULT '',
    last_used_at TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX media_keys_name ON media_keys (server_id, name);
CREATE INDEX media_keys_prefix ON media_keys (server_id, prefix);

-- One uploaded thing.
--
-- No status column. What exists in the bucket is what exists; a row describes
-- an object that was written, and an upload that failed leaves neither.
CREATE TABLE media_objects (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL REFERENCES servers (id),
    bucket_id   TEXT NOT NULL REFERENCES media_buckets (id),
    namespace   TEXT NOT NULL DEFAULT '',
    -- The path inside the bucket. Derived from the id and the original name, so
    -- two uploads called logo.png are two objects.
    object_key  TEXT NOT NULL,
    filename    TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    checksum    TEXT NOT NULL DEFAULT '',
    -- Filled in for what we can read: images get width and height, and later a
    -- PDF gets pages and a video gets a duration. Zero means "not that kind of
    -- file", not "unknown".
    width       INTEGER NOT NULL DEFAULT 0,
    height      INTEGER NOT NULL DEFAULT 0,
    -- public objects are fetched by anyone with the URL; private ones need a
    -- signed link with an expiry.
    visibility  TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    -- What checked it for malware, empty when nothing did. Kept per object
    -- rather than per bucket because the answer changes the day ClamAV is
    -- installed, and a file taken before that was still taken unchecked.
    scanner     TEXT NOT NULL DEFAULT '',
    -- Whatever the app wants to remember about it.
    metadata    TEXT NOT NULL DEFAULT '{}',
    key_id      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX media_objects_key ON media_objects (server_id, bucket_id, object_key);
CREATE INDEX media_objects_ns ON media_objects (server_id, namespace, created_at);

-- The sizes an application is allowed to ask for.
--
-- Named presets rather than a free-form query string, because a server that
-- resizes on demand for anyone who asks is a server anyone can stop with a
-- loop. An app asks for `thumb`; it cannot ask for ten thousand widths.
CREATE TABLE media_presets (
    id         TEXT PRIMARY KEY,
    server_id  TEXT NOT NULL REFERENCES servers (id),
    name       TEXT NOT NULL,
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    -- cover crops to fill, contain fits inside, and neither enlarges a small
    -- original: upscaling a thumbnail to a hero is a worse picture and a bigger
    -- file, which is both of the things this exists to avoid.
    fit        TEXT NOT NULL DEFAULT 'cover' CHECK (fit IN ('cover', 'contain')),
    format     TEXT NOT NULL DEFAULT 'auto',
    quality    INTEGER NOT NULL DEFAULT 80,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX media_presets_name ON media_presets (server_id, name);

-- Three sizes every app wants, so that a server with the service switched on is
-- immediately useful rather than immediately a form to fill in.
INSERT INTO media_presets (id, server_id, name, width, height, fit, format, quality)
SELECT lower(hex(randomblob(8))), id, 'thumb', 320, 320, 'cover', 'auto', 78 FROM servers
UNION ALL
SELECT lower(hex(randomblob(8))), id, 'card', 800, 0, 'contain', 'auto', 80 FROM servers
UNION ALL
SELECT lower(hex(randomblob(8))), id, 'hero', 1920, 0, 'contain', 'auto', 82 FROM servers;
