-- The VPN: devices and servers that may reach this one through WireGuard.
--
-- A row holds the peer's public key and never a private one. The device's key
-- is generated in the browser that adds it and leaves only as a QR code or a
-- file, so a copy of this database is not a way into the tunnel. The preshared
-- key is the one secret here, and it is encrypted like every other credential.
--
-- Whether a peer is connected is not stored: WireGuard knows, and the panel
-- asks it. first_seen_at and countries are history rather than state — what
-- the "first connection" and "new country" alerts compare against.
CREATE TABLE IF NOT EXISTS vpn_peers (
    id              TEXT PRIMARY KEY,
    server_id       TEXT NOT NULL,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL DEFAULT 'device',  -- device | server
    public_key      TEXT NOT NULL,
    psk_enc         TEXT NOT NULL,
    address         TEXT NOT NULL,                   -- 10.66.0.2
    access          TEXT NOT NULL DEFAULT 'admin',   -- admin | apps | internet | server
    ports           TEXT NOT NULL DEFAULT '',        -- for apps: 80,443
    full_tunnel     INTEGER NOT NULL DEFAULT 0,
    expires_at      TEXT NOT NULL DEFAULT '',
    fleet_server_id TEXT NOT NULL DEFAULT '',
    first_seen_at   TEXT NOT NULL DEFAULT '',
    countries       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    created_by      TEXT NOT NULL DEFAULT '',
    UNIQUE (server_id, name),
    UNIQUE (server_id, public_key),
    UNIQUE (server_id, address)
);

-- Where the controller reaches a fleet member once it has joined the VPN. Empty
-- means the public address, as before.
ALTER TABLE fleet_servers ADD COLUMN vpn_address TEXT NOT NULL DEFAULT '';
