-- Protection that can name a path and a person.
--
-- Until now "protect with Islet login" was one switch per host, open to every
-- signed-in account. Two things people asked for do not fit that: a site where
-- only the staging path should ask for a login, and a site where only some of
-- the panel's users should get in. Both are the same missing axis — the rule
-- needs somewhere to say *which path* and *which people*.
--
-- protect_users is a comma-separated list of usernames, and empty keeps
-- today's meaning: any signed-in Islet user. A list rather than a join table
-- because it is written and read whole, exactly like basic_auth and
-- ip_allowlist beside it, and a join table would buy referential integrity
-- over a name that is already the key everywhere else in the product.
ALTER TABLE domains ADD COLUMN protect_users TEXT NOT NULL DEFAULT '';

-- A location's protection is three-state, not a boolean: 'inherit' follows the
-- host, 'on' asks for a login (with a list of its own, so /admin can be
-- narrower than the site around it), and 'off' opens a path back up on a
-- protected host — which is what a payment callback or a webhook receiver
-- needs, and what a boolean could not express at all.
ALTER TABLE domain_locations ADD COLUMN protect TEXT NOT NULL DEFAULT 'inherit';
ALTER TABLE domain_locations ADD COLUMN protect_users TEXT NOT NULL DEFAULT '';
