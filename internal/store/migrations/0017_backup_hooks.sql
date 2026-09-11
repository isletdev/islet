-- Backup plans: shell hooks around the snapshot and pausing containers that use the volumes.
ALTER TABLE backup_plans ADD COLUMN pre_cmd TEXT NOT NULL DEFAULT '';
ALTER TABLE backup_plans ADD COLUMN post_cmd TEXT NOT NULL DEFAULT '';
ALTER TABLE backup_plans ADD COLUMN pause INTEGER NOT NULL DEFAULT 0;
