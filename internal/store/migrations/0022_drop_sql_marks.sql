-- The SQL client's environment marking is gone: a connection is a
-- connection, and three tiers where only one changed any behaviour was a
-- taxonomy rather than a feature. Statements that are dangerous on their own
-- terms still ask for confirmation, on every connection.
DROP TABLE IF EXISTS sql_marks;
