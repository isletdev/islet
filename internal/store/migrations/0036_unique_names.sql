-- Three collections could hold two things with the same name. Nine others
-- could not.
--
-- apps, domains, backup destinations, backup plans, runner pools, workspaces,
-- vault secrets, SQL connections and AI providers all refuse a duplicate name.
-- checks, channels and jobs had no unique constraint at all, which is a
-- difference nobody decided — and it is also what makes a retried POST safe or
-- unsafe, since a create that cannot make a duplicate is a create you can send
-- twice. The sharpest case is cron: a retried request left two jobs on the same
-- schedule, quietly running the same command twice, forever.
--
-- Scoped to the server, not global, because that is the rule the newer tables
-- follow and the one a fleet needs.

-- Existing rows first. A daemon that has been running for a year may already
-- have two channels called "ops", and a migration that fails leaves the daemon
-- in a restart loop with the reason only in the journal — so this renames
-- rather than refuses. The suffix is the row's own id, which is unique by
-- construction and tells somebody looking at the list exactly which row was
-- touched.
UPDATE checks
SET name = name || ' (' || substr(id, 1, 6) || ')'
WHERE rowid NOT IN (SELECT min(rowid) FROM checks GROUP BY server_id, name);

UPDATE channels
SET name = name || ' (' || substr(id, 1, 6) || ')'
WHERE rowid NOT IN (SELECT min(rowid) FROM channels GROUP BY server_id, name);

UPDATE jobs
SET name = name || ' (' || substr(id, 1, 6) || ')'
WHERE rowid NOT IN (SELECT min(rowid) FROM jobs GROUP BY server_id, name);

CREATE UNIQUE INDEX checks_name ON checks (server_id, name);
CREATE UNIQUE INDEX channels_name ON channels (server_id, name);
CREATE UNIQUE INDEX jobs_name ON jobs (server_id, name);
