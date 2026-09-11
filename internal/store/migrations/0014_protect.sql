-- "Protect with Islet login": routes can require a panel session (forward auth).
ALTER TABLE domains ADD COLUMN protect INTEGER NOT NULL DEFAULT 0;
