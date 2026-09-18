-- Five indexes for five queries that were reading whole tables.
--
-- None of this changes behaviour. Each one is a query plan that said SCAN or
-- USE TEMP B-TREE against a table with no ceiling on it, on a machine specified
-- as 1 vCPU and 1 GB, on pages people open every day.

-- The uptime aggregates and their purge.
--
-- check_results is structurally the largest table here: one row per check per
-- interval, so a single monitor at sixty seconds writes 1,440 rows a day and
-- twenty of them reach three quarters of a million rows a month. The existing
-- index is (check_id, id DESC), which serves "the last few results" and does
-- nothing for "how many of the last 24 hours were ok" — that filters on `at`,
-- which appears in no index at all, so every percentage read every retained row
-- for that check and threw most of them away. The panel asks for those
-- percentages from every page, every sixty seconds, through /attention.
--
-- The purge had the same problem from the other end: DELETE ... WHERE at < ?
-- with no index on `at` is a full scan of the biggest table, every six hours.
CREATE INDEX check_results_at ON check_results (check_id, at);

-- Notification deliveries, by the event they belong to.
--
-- deliveries was indexed only on (status, next_try_at), which is what the send
-- loop wants. Two other things ask for it by event: the page that shows where
-- one notification went, and — the expensive one — the nightly prune of old
-- events, where every deleted row makes SQLite look for children to cascade to
-- and finds them with a full scan of deliveries. One index turns a nightly
-- O(events x deliveries) into a range read.
CREATE INDEX deliveries_event ON deliveries (event_id);

-- And by channel, for the digest sweep, which asks what is pending per channel.
CREATE INDEX deliveries_channel ON deliveries (channel_id, status);

-- The audit log and the command drawer, in the order they are actually read.
--
-- Both are indexed on (server_id, created_at), which is right for the retention
-- sweep and wrong for every page load: the list queries order by id DESC, so
-- SQLite sorted the result in a temp b-tree instead of walking an index. Both
-- tables are capped at 200,000 rows, so the sort was bounded — and bounded at
-- 200,000 rows, twice, on two of the most-visited pages in the panel.
--
-- These are added rather than replacing the existing ones, which still serve
-- the prune-by-age that keeps both tables inside their caps.
CREATE INDEX audit_log_server_id ON audit_log (server_id, id DESC);
CREATE INDEX commands_server_id ON commands (server_id, id DESC);
