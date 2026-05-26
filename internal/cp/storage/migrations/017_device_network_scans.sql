-- +goose Up

-- Per-request rows for the Phase 2 network-scan flow (issue #3). One
-- row per operator-triggered scan, keyed by correlation_id (the same
-- ID minted CP-side, sent down the cmd channel, and echoed by the
-- agent in cmd-result). Status walks pending → done | error as the
-- agent's ACK lands; the success payload is a JSONB column carrying
-- the {hosts: [...]} structure (small — at most a few hundred hosts on
-- a store LAN). Rows stay readable until the sweeper reaps them.
--
-- Same posture as device_log_tails: text status (not enum) so a future
-- state can be added without a migration; CASCADE on device delete.
CREATE TABLE device_network_scans (
    correlation_id  text        PRIMARY KEY,
    device_id       uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    cidr_requested  text,                    -- NULL = auto-detect requested
    status          text        NOT NULL,    -- pending | done | error
    result          jsonb,                   -- Response shape; NULL until ACK
    error_code      text,
    error_message   text,
    requested_at    timestamptz NOT NULL,
    returned_at     timestamptz
);

-- Per-device, latest-first index for "show this device's scan history"
-- (the dashboard polls for one correlation_id at a time but a future
-- per-device history surface gets the index for free).
CREATE INDEX device_network_scans_by_device
    ON device_network_scans (device_id, requested_at DESC);

-- Sweeper target. The cleanup goroutine runs
--   DELETE FROM device_network_scans WHERE requested_at < now() - interval '24 hours'
-- — this index keeps that bounded.
CREATE INDEX device_network_scans_old ON device_network_scans (requested_at);

-- +goose Down

DROP TABLE device_network_scans;
