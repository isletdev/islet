-- The last of the environment tiers.
--
-- 0022 dropped the marks table; the column on the connection itself outlived
-- it, defaulting every row to "development" and being read by nothing. A
-- connection is a connection: what it is for is the person's business, not a
-- field the panel validates.
ALTER TABLE sql_connections DROP COLUMN environment;
