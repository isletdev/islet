-- People who may sign in to a protected site and to nothing else.
--
-- They are not rows in users, on purpose. Every check that admits somebody to
-- the panel, the API, the terminal or the file manager reads users, so a person
-- kept out of that table cannot reach any of it by a route somebody forgot to
-- close. A role on users would have needed "except site people" at each of
-- those checks, and one of them would eventually be missed.
--
-- A site session belongs to one host. The cookie is host-only as well, but the
-- column is what stops an application that has seen a visitor's cookie from
-- replaying it at another protected site.
CREATE TABLE IF NOT EXISTS site_users (
    id               TEXT PRIMARY KEY,
    server_id        TEXT NOT NULL,
    username         TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    password_hash    TEXT NOT NULL DEFAULT '',       -- empty until set or an invite is accepted
    totp_secret_enc  BLOB,
    totp_pending_enc BLOB,
    totp_last_step   INTEGER NOT NULL DEFAULT 0,
    require_totp     INTEGER NOT NULL DEFAULT 0,
    disabled         INTEGER NOT NULL DEFAULT 0,
    invite_hash      TEXT NOT NULL DEFAULT '',
    invite_expires   TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    created_by       TEXT NOT NULL DEFAULT '',
    last_login_at    TEXT NOT NULL DEFAULT '',
    UNIQUE (server_id, username)
);

CREATE TABLE IF NOT EXISTS site_sessions (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    site_user_id TEXT NOT NULL REFERENCES site_users (id) ON DELETE CASCADE,
    host         TEXT NOT NULL,
    mfa_pending  INTEGER NOT NULL DEFAULT 0,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS site_sessions_user ON site_sessions (site_user_id);

-- How a protected domain's sign-in page looks. One row per domain, and none
-- for a domain that keeps the default.
CREATE TABLE IF NOT EXISTS site_login_pages (
    domain_id  TEXT PRIMARY KEY REFERENCES domains (id) ON DELETE CASCADE,
    server_id  TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL DEFAULT '',
    logo       TEXT NOT NULL DEFAULT '',   -- a data: URI, so the page loads nothing from anywhere
    accent     TEXT NOT NULL DEFAULT '',   -- #rrggbb
    theme      TEXT NOT NULL DEFAULT 'auto',
    css        TEXT NOT NULL DEFAULT '',
    -- Whether the page offers panel accounts a way through the panel. Off,
    -- the page says nothing about Islet at all, which is what a client portal
    -- wants; a panel account can still get in by signing in to the panel.
    panel_link INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
