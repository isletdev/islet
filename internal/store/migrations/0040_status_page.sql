-- Which checks a stranger may see.
--
-- A status page is the one thing in this panel written for people who are not
-- the operator: the customer wondering whether it is them or the site. So it is
-- opt-in per check and off by default, because the list of what a server
-- monitors is itself information — half of these checks are internal, and a
-- check's target is a URL nobody outside needs.
--
-- An ordinary ADD COLUMN: nothing here is a constraint that has to be rebuilt.
ALTER TABLE checks ADD COLUMN public INTEGER NOT NULL DEFAULT 0;
