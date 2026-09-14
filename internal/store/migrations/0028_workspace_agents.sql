-- Several agents in one workspace, each with a conversation of its own.
--
-- A workspace was one directory and one command, which makes the obvious thing
-- impossible: one agent writing code while another runs the tests against it,
-- in the same checkout, watched side by side. tmux already models this — a
-- session holds windows — so an agent becomes a window and the workspace stays
-- the session. Nothing about survival changes.
--
-- session_uuid is the part that makes resuming honest. `claude --continue`
-- reopens the most recent conversation *for a directory*, so two agents sharing
-- a checkout would both resume the same one and one of them would silently
-- inherit the other's history. Claude Code takes `--session-id <uuid>` on the
-- first run and `--resume <uuid>` afterwards, so each agent is pinned to its
-- own conversation and coming back after a crash, a close or a reboot is exact
-- rather than a guess at what "most recent" meant.

CREATE TABLE workspace_agents (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL REFERENCES servers (id),
    workspace_id TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- Also the tmux window name, so a person attaching over SSH sees the same
    -- names the panel shows.
    name         TEXT NOT NULL,
    preset       TEXT NOT NULL DEFAULT 'claude' CHECK (preset IN ('claude', 'shell', 'custom')),
    command      TEXT NOT NULL DEFAULT '',
    -- 1: come back into the same conversation after a stop, a crash or a
    -- reboot, instead of starting an empty one.
    resume       INTEGER NOT NULL DEFAULT 1,
    -- The conversation this agent owns. Generated once, never reused across
    -- agents.
    session_uuid TEXT NOT NULL DEFAULT '',
    -- 1: --dangerously-skip-permissions. Per agent, because a worker and a
    -- reviewer in one workspace should not be forced to the same answer.
    skip_permissions INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    -- Empty until it has run once. That is what decides between --session-id
    -- and --resume, so it is a fact about the conversation, not a statistic.
    last_started_at TEXT NOT NULL DEFAULT '',
    UNIQUE (workspace_id, name)
);

CREATE INDEX workspace_agents_ws ON workspace_agents (workspace_id, name);

-- Every workspace that had a command becomes one agent running it, so nothing
-- that exists today is lost or needs recreating by hand. A workspace whose
-- preset was `shell` had no command and gets no agent: it was already just a
-- shell, and every workspace now has one of those.
INSERT INTO workspace_agents (id, server_id, workspace_id, name, preset, command, skip_permissions, resume)
SELECT lower(hex(randomblob(6))), server_id, id,
       CASE preset WHEN 'claude' THEN 'claude' ELSE 'main' END,
       preset, command, skip_permissions, 1
FROM workspaces
WHERE command <> '';
