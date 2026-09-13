-- Custom locations, and a certificate method that is a choice rather than a
-- consequence.
--
-- Two gaps against what people arrive from. Nginx Proxy Manager lets one
-- hostname forward several paths to several backends, and Islet allowed one
-- target per host, so importing such a host silently lost everything but the
-- root. And DNS-01 was reachable only by asking for a wildcard, although the
-- provider credentials that make it work are the same either way — which
-- matters for any host whose port 80 is not reachable, the usual case being a
-- CDN in front of it.

-- SQLite cannot alter a CHECK constraint, so the table is rebuilt to admit the
-- new tls value. Nothing references domains yet, which is why this is safe to
-- do before the locations table below exists.
CREATE TABLE domains_new (
    id            TEXT PRIMARY KEY,
    server_id     TEXT NOT NULL REFERENCES servers (id),
    host          TEXT NOT NULL UNIQUE,
    target_type   TEXT NOT NULL CHECK (target_type IN ('container', 'panel', 'url')),
    target        TEXT NOT NULL DEFAULT '',
    port          INTEGER NOT NULL DEFAULT 80,
    path_prefix   TEXT NOT NULL DEFAULT '',
    tls           TEXT NOT NULL DEFAULT 'letsencrypt'
                  CHECK (tls IN ('letsencrypt', 'letsencrypt-dns', 'self', 'none')),
    redirect_www  INTEGER NOT NULL DEFAULT 0,
    basic_auth    TEXT NOT NULL DEFAULT '',
    ip_allowlist  TEXT NOT NULL DEFAULT '',
    rate_limit    INTEGER NOT NULL DEFAULT 0,
    headers       TEXT NOT NULL DEFAULT '',
    maintenance   INTEGER NOT NULL DEFAULT 0,
    protect       INTEGER NOT NULL DEFAULT 0,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO domains_new
    (id, server_id, host, target_type, target, port, path_prefix, tls, redirect_www,
     basic_auth, ip_allowlist, rate_limit, headers, maintenance, protect, enabled,
     created_at, updated_at)
SELECT id, server_id, host, target_type, target, port, path_prefix,
       -- A wildcard has never had another way to be issued, so it was already
       -- DNS-01 in everything but the name of the value stored here.
       CASE WHEN tls = 'letsencrypt' AND host LIKE '*.%' THEN 'letsencrypt-dns' ELSE tls END,
       redirect_www, basic_auth, ip_allowlist, rate_limit, headers, maintenance,
       protect, enabled, created_at, updated_at
FROM domains;

DROP TABLE domains;
ALTER TABLE domains_new RENAME TO domains;
CREATE INDEX domains_server ON domains (server_id);

-- One extra path on a host, forwarded somewhere of its own. The root route
-- stays on the domain row: a location is an exception to it, not a
-- replacement, which is also how the products people import from model it.
CREATE TABLE domain_locations (
    id          TEXT PRIMARY KEY,
    domain_id   TEXT NOT NULL REFERENCES domains (id) ON DELETE CASCADE,
    path        TEXT NOT NULL,                    -- /api
    target_type TEXT NOT NULL CHECK (target_type IN ('container', 'panel', 'url')),
    target      TEXT NOT NULL DEFAULT '',
    port        INTEGER NOT NULL DEFAULT 80,
    -- 1: /api/things reaches the backend as /things, which is what nginx does
    -- with a trailing slash on proxy_pass and Caddy with handle_path.
    strip_path  INTEGER NOT NULL DEFAULT 0,
    position    INTEGER NOT NULL DEFAULT 0,
    UNIQUE (domain_id, path)
);

CREATE INDEX domain_locations_domain ON domain_locations (domain_id, position);
