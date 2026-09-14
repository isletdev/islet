-- Accounts nobody can sign in to, so the score stops asking them for 2FA.
--
-- Adopting a server into a fleet creates islet-controller: an admin account
-- that exists so the audit log can say an action arrived from the controlling
-- panel rather than attributing it to a person. Its password is random, never
-- printed and never used — the account is reached only by an API token.
--
-- "Two-factor on every admin account" counted it anyway. Nobody can log in to
-- enrol a secret for it, so the check could never pass again on any server that
-- had been adopted, and a maintainer with 2FA correctly enabled on their own
-- account saw a permanent ten-point failure with no fix available anywhere in
-- the panel. Marking the account for what it is keeps the check about the
-- humans who can actually hold a second factor.
ALTER TABLE users ADD COLUMN is_service INTEGER NOT NULL DEFAULT 0 CHECK (is_service IN (0, 1));

UPDATE users SET is_service = 1 WHERE username = 'islet-controller';
