-- Events name the thing they are about (app, container, check, job, plan) so
-- channels can be limited to a few subjects; the panel sidebar can embed apps.
ALTER TABLE events ADD COLUMN subject TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN subjects TEXT NOT NULL DEFAULT '';
