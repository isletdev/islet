-- Work that takes longer than a request.
--
-- Called `work` rather than `jobs` because `jobs` is cron's, and has been since
-- 0007. A scheduled command and a queued transcode are different enough that
-- sharing a word for them would cost somebody an afternoon eventually.
--
-- Everything this daemon does has so far been either instant enough to answer in
-- the request that asked for it, or a run belonging to one feature with its own
-- table: a deploy, a backup, an assistant run. Transcoding a video is neither.
-- It is minutes of one core, it can fail halfway, somebody wants to know how far
-- along it is, and on a 1 vCPU box two of them at once is the difference between
-- a slow server and an unreachable one.
--
-- So: one table, one worker, one job at a time. Not a job runner framework —
-- there is no cron here, no fan-out, no dependencies, no priorities. Those are
-- all things to add when something needs them, and the thing that needs a queue
-- today needs a line.
--
-- Why durable rather than a channel in memory: a transcode outlives an `islet
-- update`, and losing the queue on restart would mean a person's upload silently
-- never becoming a video. What cannot survive a restart is the work itself —
-- ffmpeg does not resume — so a job found running at startup is failed with that
-- as its reason, which is the truth and can be retried, rather than left to look
-- as though it were still going.
CREATE TABLE work (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL,
    -- What kind of work: 'media.transcode' today. The handler for a kind is
    -- registered in Go; a row whose kind nothing handles is failed on sight
    -- rather than left queued forever by a daemon that no longer knows how.
    kind         TEXT NOT NULL,
    -- The job's own arguments, as JSON. Opaque here on purpose: the queue knows
    -- nothing about media and media knows nothing about SQL.
    payload      TEXT NOT NULL DEFAULT '{}',
    -- queued | running | done | failed | cancelled
    state        TEXT NOT NULL DEFAULT 'queued',
    -- 0 to 1, and what it is doing right now, both for the person watching.
    progress     REAL NOT NULL DEFAULT 0,
    detail       TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    -- What this job is about, so the panel can show it beside the thing rather
    -- than in a list of ids: 'media:object:<id>'. Free text, never parsed here.
    subject      TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    actor        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at   TEXT NOT NULL DEFAULT '',
    finished_at  TEXT NOT NULL DEFAULT ''
);

-- The two ways this is read: what to run next, and what is going on.
CREATE INDEX work_queue   ON work (server_id, state, created_at);
CREATE INDEX work_subject ON work (server_id, subject, created_at DESC);
