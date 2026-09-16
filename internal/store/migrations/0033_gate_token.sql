-- A second credential for the forward-auth gate, so that protecting a site
-- stops meaning "hand that site the panel".
--
-- The session cookie had to be scoped to a parent domain for the gate to see
-- it on the app's hostname — and a reverse proxy forwards Cookie to the
-- backend like any other header. So every protected app received a token that
-- was the panel, as whoever was visiting: an admin's session in the request log
-- of any application behind the gate.
--
-- The gate token is a different random value stored beside the session it
-- belongs to. It rides on its own cookie, scoped to the parent domain, and the
-- panel accepts it at /_islet/auth and nowhere else; the session cookie goes
-- back to being host-only. An application behind the gate now holds something
-- that proves who the visitor is to the gate and opens nothing at the panel.
--
-- Empty means this session predates the column, or is still waiting on a second
-- factor. Both mint one when they need it, so nobody is signed out by an update.
ALTER TABLE sessions ADD COLUMN gate_hash TEXT NOT NULL DEFAULT '';

-- Not UNIQUE: every session that has no gate token yet carries the same empty
-- string, and a unique index would refuse the second one.
CREATE INDEX sessions_gate ON sessions (gate_hash);
