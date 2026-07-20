-- 002_station_devices.sql
-- Device call-next layer: transport-agnostic hardware buttons.
-- A station_devices row = the network-terminating unit (WiFi button, PC dongle
-- bridge, or 4G cellular gateway). A station_buttons row = one logical button on
-- that device; a standalone WiFi button is the degenerate one-button case.
-- Idempotent: safe to re-run (IF NOT EXISTS throughout).
-- Apply manually: repository.Migrate only bootstraps 001 on an empty database.

CREATE TABLE IF NOT EXISTS station_devices (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL REFERENCES tenants(id),
    location_id  UUID        NOT NULL REFERENCES locations(id),
    label        TEXT        NOT NULL,
    token_hash   BYTEA       NOT NULL UNIQUE,   -- SHA-256 of the device secret; plaintext never stored
    is_active    BOOLEAN     NOT NULL DEFAULT true,
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS station_buttons (
    id              UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id       UUID  NOT NULL REFERENCES station_devices(id),
    button_code     TEXT  NOT NULL,             -- e.g. 'B1'; fixed in firmware for standalone buttons
    staff_member_id UUID  REFERENCES staff_members(id),  -- NULL = pooled/shop-wide dispatch
    label           TEXT  NOT NULL,
    UNIQUE (device_id, button_code)
);

-- token_hash lookups are the auth hot path; UNIQUE above already indexes it,
-- this named index documents intent and survives a future constraint rename.
CREATE INDEX IF NOT EXISTS idx_station_devices_token_hash ON station_devices (token_hash);
CREATE INDEX IF NOT EXISTS idx_station_buttons_device_code ON station_buttons (device_id, button_code);
