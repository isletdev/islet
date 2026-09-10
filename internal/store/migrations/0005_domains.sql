-- Domains routed by the managed Traefik proxy.

CREATE TABLE domains (
    id            TEXT PRIMARY KEY,
    server_id     TEXT NOT NULL REFERENCES servers (id),
    host          TEXT NOT NULL UNIQUE,            -- app.example.com
    target_type   TEXT NOT NULL CHECK (target_type IN ('container', 'panel', 'url')),
    target        TEXT NOT NULL DEFAULT '',         -- container name, or a URL for target_type url
    port          INTEGER NOT NULL DEFAULT 80,
    path_prefix   TEXT NOT NULL DEFAULT '',         -- route only this prefix, e.g. /api
    tls           TEXT NOT NULL DEFAULT 'letsencrypt' CHECK (tls IN ('letsencrypt', 'self', 'none')),
    redirect_www  INTEGER NOT NULL DEFAULT 0,       -- 1: www.host -> host
    basic_auth    TEXT NOT NULL DEFAULT '',         -- user:bcrypt-hash, one per line
    ip_allowlist  TEXT NOT NULL DEFAULT '',         -- CIDRs, comma separated
    rate_limit    INTEGER NOT NULL DEFAULT 0,       -- requests per second, 0 = off
    headers       TEXT NOT NULL DEFAULT '',         -- Name: value lines added to responses
    maintenance   INTEGER NOT NULL DEFAULT 0,       -- 1: serve the maintenance page
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX domains_server ON domains (server_id);
