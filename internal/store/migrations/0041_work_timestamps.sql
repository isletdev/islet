-- Make the timestamps already in the work queue sort the way new ones do.
--
-- v0.35.0 changed the queue to write a fixed-width timestamp, because
-- RFC3339Nano trims trailing zeros and the queue orders by that column as a
-- string: '…:09.5Z' sorts after '…:09.50001Z', and a whole second sorts after
-- everything inside it. New rows are right; the rows already there are not, and
-- on a server that has run a transcode the table is a mix of both. A release
-- check found three width mismatches in this very database, each of which
-- compares backwards.
--
-- Nine digits, always. A value with a fractional part is padded to nine; one
-- without gains '.000000000'. Empty stays empty, because that is how this table
-- says "has not started" and "has not finished".
--
-- The column's DEFAULT still writes the short form, and is left alone: nothing
-- inserts without supplying these three columns, and changing a default means
-- rebuilding the table for no behaviour anybody would see.
UPDATE work SET created_at = CASE
    WHEN created_at LIKE '%.%' THEN substr(created_at, 1, length(created_at) - 1) || substr('000000000', 1, 30 - length(created_at)) || 'Z'
    ELSE substr(created_at, 1, 19) || '.000000000Z'
  END
 WHERE created_at <> '' AND length(created_at) <> 30;

UPDATE work SET started_at = CASE
    WHEN started_at LIKE '%.%' THEN substr(started_at, 1, length(started_at) - 1) || substr('000000000', 1, 30 - length(started_at)) || 'Z'
    ELSE substr(started_at, 1, 19) || '.000000000Z'
  END
 WHERE started_at <> '' AND length(started_at) <> 30;

UPDATE work SET finished_at = CASE
    WHEN finished_at LIKE '%.%' THEN substr(finished_at, 1, length(finished_at) - 1) || substr('000000000', 1, 30 - length(finished_at)) || 'Z'
    ELSE substr(finished_at, 1, 19) || '.000000000Z'
  END
 WHERE finished_at <> '' AND length(finished_at) <> 30;
