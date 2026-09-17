-- Models as things you set up once, rather than as one global setting.
--
-- The assistant had a single provider: one kind, one key, one model, in the
-- settings table. Workspaces had no concept of a provider at all — an agent ran
-- a command, and the command happened to be Claude Code. Neither could answer
-- the question people actually have, which is "which of the models I pay for
-- should this conversation use".
--
-- So a provider becomes a row somebody can name. The assistant picks one per
-- chat, a workspace agent picks one when it is created, and anything that
-- already exists keeps working by pointing at nothing and getting the default.
CREATE TABLE ai_providers (
    id         TEXT PRIMARY KEY,
    server_id  TEXT NOT NULL REFERENCES servers (id),
    -- What a person calls it: "Claude subscription", "Work Anthropic key".
    -- Two of the same kind is the normal case, which is why the name matters.
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('anthropic', 'openai', 'subscription')),
    model      TEXT NOT NULL DEFAULT '',
    base_url   TEXT NOT NULL DEFAULT '',
    -- Sealed exactly as the settings table sealed it — the same encryption, the
    -- same base64 — so the copy below moves the credential without ever
    -- decrypting it, and without anybody having to paste a key again.
    api_key    TEXT NOT NULL DEFAULT '',
    -- For the subscription kind: where Claude Code is, and the MCP file that
    -- gives it Islet's tools. Empty means "wherever it is installed".
    command    TEXT NOT NULL DEFAULT '',
    mcp_config TEXT NOT NULL DEFAULT '',
    is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX ai_providers_name ON ai_providers (server_id, name);

-- Which provider a conversation is having. Empty means the default, which is
-- what every conversation that predates this column has — so nothing that is
-- open right now changes mid-sentence.
ALTER TABLE assistant_chats ADD COLUMN provider_id TEXT NOT NULL DEFAULT '';

-- Same for an agent. The command is still what actually runs; this records
-- which provider filled it in, so the panel can say so and offer the same
-- choice again.
ALTER TABLE workspace_agents ADD COLUMN provider_id TEXT NOT NULL DEFAULT '';

-- Carry the existing configuration over, sealed key and all.
--
-- The point of this migration is that nobody sets Claude up twice. Whatever the
-- assistant was using becomes a provider row, named for what it is, and marked
-- default so every existing chat — which points at nothing — resolves to
-- exactly what it was using yesterday.
--
-- The WHERE is the whole safety of it: on a server that never configured an
-- assistant, this inserts nothing and the panel shows an empty list.
INSERT INTO ai_providers (id, server_id, name, kind, model, base_url, api_key, command, mcp_config, is_default)
SELECT
    lower(hex(randomblob(8))),
    (SELECT id FROM servers ORDER BY rowid LIMIT 1),
    CASE (SELECT value FROM settings WHERE key = 'assistant.provider')
        WHEN 'subscription' THEN 'Claude subscription'
        WHEN 'openai'       THEN 'OpenAI-compatible'
        ELSE 'Anthropic'
    END,
    COALESCE(NULLIF((SELECT value FROM settings WHERE key = 'assistant.provider'), ''), 'anthropic'),
    COALESCE((SELECT value FROM settings WHERE key = 'assistant.model'), ''),
    COALESCE((SELECT value FROM settings WHERE key = 'assistant.base_url'), ''),
    COALESCE((SELECT value FROM settings WHERE key = 'assistant.key'), ''),
    COALESCE((SELECT value FROM settings WHERE key = 'assistant.claude_bin'), ''),
    COALESCE((SELECT value FROM settings WHERE key = 'assistant.mcp_config'), ''),
    1
WHERE EXISTS (SELECT 1 FROM servers)
  AND (
    -- Configured means a key was stored, or a provider was chosen explicitly.
    -- A row that holds only a model nobody picked is not a setup worth copying.
    COALESCE((SELECT value FROM settings WHERE key = 'assistant.key'), '') <> ''
    OR COALESCE((SELECT value FROM settings WHERE key = 'assistant.provider'), '') <> ''
  );
