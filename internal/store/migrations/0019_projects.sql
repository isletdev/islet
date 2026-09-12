-- Project scopes: a deployer or viewer with a projects list only sees the
-- apps whose names match (globs), their containers, databases and domains.
ALTER TABLE users ADD COLUMN projects TEXT NOT NULL DEFAULT '';
