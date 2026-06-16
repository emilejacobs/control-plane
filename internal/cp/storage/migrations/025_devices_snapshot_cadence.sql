-- +goose Up

-- Per-device scheduled-snapshot cadence (#9, ADR-030 § 7). The agent's snapshot
-- scheduler (a later slice) fires every camera on the device at this cadence.
-- 'weekly' is the default the feature ships with; 'off' disables scheduled
-- snapshots for a device; 'daily' is the higher-frequency opt-in. text + CHECK
-- (not a PG enum) so a future cadence is an additive migration, not ALTER TYPE
-- (same rationale as device_services.state / device_captures.kind).
ALTER TABLE devices ADD COLUMN snapshot_cadence text NOT NULL DEFAULT 'weekly'
    CHECK (snapshot_cadence IN ('off', 'daily', 'weekly'));

-- +goose Down
ALTER TABLE devices DROP COLUMN snapshot_cadence;
