-- +goose Up

-- Offline-reason tracking (PRD .scratch/offline-reason-tracking, #157). The
-- agent reports its system boot_time + previous-shutdown cause on the heartbeat;
-- cp-ingest compares the reported boot_time to the device's stored value to tell
-- a reboot (changed) from a network/MQTT blip (unchanged). These columns hold
-- the device's most-recent boot state — the comparison anchor + the device-page
-- "last boot / last shutdown cause" surface (#159). NULL until the first
-- boot-info-bearing heartbeat from a rolled agent.
ALTER TABLE devices ADD COLUMN last_boot_time           timestamptz;
ALTER TABLE devices ADD COLUMN last_shutdown_cause      text;
ALTER TABLE devices ADD COLUMN last_shutdown_cause_code integer;

-- Per-device reboot history: one row per distinct boot_time observed. Drives
-- the "is this device rebooting too often?" investigation (#159) and the
-- offline-reason label on recovery (#158, which reads rows inside the offline
-- window). detected_at is when CP first saw the new boot_time, not the boot
-- instant (boot_time). First contact for a device is recorded here too, but is
-- not alerted as a reboot downstream.
CREATE TABLE device_reboots (
    id                  bigserial   PRIMARY KEY,
    device_id           uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    boot_time           timestamptz NOT NULL,
    shutdown_cause      text,
    shutdown_cause_code integer,
    detected_at         timestamptz NOT NULL
);

-- Idempotency boundary: at most one row per (device, boot_time). A repeat
-- heartbeat carrying the same boot_time is a no-op (ON CONFLICT DO NOTHING),
-- so re-delivery / replay never double-counts a reboot.
CREATE UNIQUE INDEX device_reboots_device_boot ON device_reboots (device_id, boot_time);

-- Per-device, newest-first — the device-page history list (#159) and the
-- offline-window lookup (#158).
CREATE INDEX device_reboots_by_device ON device_reboots (device_id, detected_at DESC);

-- +goose Down
DROP TABLE device_reboots;
ALTER TABLE devices DROP COLUMN last_shutdown_cause_code;
ALTER TABLE devices DROP COLUMN last_shutdown_cause;
ALTER TABLE devices DROP COLUMN last_boot_time;
