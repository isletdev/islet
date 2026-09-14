-- Whether a Claude Code workspace runs with --dangerously-skip-permissions.
--
-- Stored rather than folded into `command` so the panel can say plainly what it
-- means, and so turning it off is a checkbox rather than editing a command line
-- and hoping you removed the right words. The flag is the difference between an
-- agent that asks before it acts and one that does not, on a machine serving
-- real sites; that is worth a field of its own.
ALTER TABLE workspaces ADD COLUMN skip_permissions INTEGER NOT NULL DEFAULT 0;
