-- Conversations with the assistant, kept where the work is.
--
-- They were in the browser: a transcript in React state, gone on reload, and
-- never on the other device. That is wrong for this feature in particular —
-- what the assistant did is a record of changes to a real server, alongside the
-- audit log rather than inside one tab's memory. Keeping it here means picking
-- a conversation up on a phone that a laptop started, and seeing what was asked
-- last week without having had that window open ever since.
--
-- A message is stored as the JSON of one turn rather than as columns, because a
-- turn is a small tree: text, the tool calls it asked for, the results that came
-- back. Splitting that into tables would buy queries nobody runs — the panel
-- reads a conversation whole, in order — and would need a migration every time
-- a provider gains a field.
--
-- server_id is on both, because every machine-bound table carries one so a
-- fleet never needs a schema rewrite.
CREATE TABLE assistant_chats (
    id         TEXT PRIMARY KEY,
    server_id  TEXT NOT NULL,
    username   TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Newest first, per person, which is the only way this is ever listed.
CREATE INDEX assistant_chats_owner ON assistant_chats (server_id, username, updated_at DESC);

CREATE TABLE assistant_messages (
    chat_id    TEXT NOT NULL REFERENCES assistant_chats (id) ON DELETE CASCADE,
    server_id  TEXT NOT NULL,
    seq        INTEGER NOT NULL,
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (chat_id, seq)
);
