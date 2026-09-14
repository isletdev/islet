-- Workspaces: a directory, a command, and a session that outlives the panel.
--
-- The point of the feature is that the work does not stop when the browser
-- does. Islet's terminal starts a PTY inside the WebSocket handler and kills it
-- when the handler returns, so a refresh, a navigation or an `islet update`
-- takes the shell with it. A workspace is backed by a tmux session instead, so
-- the process is a child of tmux rather than of isletd and survives all three —
-- and can still be reached with `tmux attach` over SSH, without Islet at all.
--
-- There is no status column on purpose. Whether a session is running is a fact
-- about tmux, and asking tmux is the only answer that cannot go stale; the
-- runner feature reads its state back off Docker labels for the same reason.

CREATE TABLE workspaces (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL REFERENCES servers (id),
    name        TEXT NOT NULL,
    directory   TEXT NOT NULL,
    -- claude | shell | custom. The preset decides what Start sends; `command`
    -- is what actually gets typed, so a preset is a convenience and never a
    -- second source of truth.
    preset      TEXT NOT NULL DEFAULT 'shell' CHECK (preset IN ('claude', 'shell', 'custom')),
    command     TEXT NOT NULL DEFAULT '',
    -- 1: give the agent an Islet API token and point it at this panel's MCP
    -- endpoint, so it can read logs and act on containers through audited,
    -- scoped calls instead of shelling out.
    mcp_enabled INTEGER NOT NULL DEFAULT 0,
    -- The token minted for that, so deleting the workspace can revoke it.
    mcp_token_id TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_attached_at TEXT NOT NULL DEFAULT '',
    UNIQUE (server_id, name)
);

CREATE INDEX workspaces_server ON workspaces (server_id, name);
