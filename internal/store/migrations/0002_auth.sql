-- Users, sessions and two-factor state.

CREATE TABLE users (
    id                  TEXT PRIMARY KEY,
    username            TEXT NOT NULL UNIQUE,
    password_hash       TEXT NOT NULL,
    role                TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'deployer', 'viewer')),
    totp_secret_enc     BLOB,                     -- AES-GCM encrypted, NULL when 2FA is off
    totp_pending_enc    BLOB,                     -- secret awaiting confirmation during setup
    totp_last_step      INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_login_at       TEXT
);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,                 -- random id shown in the sessions list
    token_hash  TEXT NOT NULL UNIQUE,             -- sha256 of the cookie value
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    mfa_pending INTEGER NOT NULL DEFAULT 0 CHECK (mfa_pending IN (0, 1)),
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at  TEXT NOT NULL
);

CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE recovery_codes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    TEXT
);

CREATE INDEX recovery_codes_user ON recovery_codes (user_id);
