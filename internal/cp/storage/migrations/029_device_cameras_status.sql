-- +goose Up

-- Per-camera reachability status (#112, PRD #111 Camera observability).
-- The agent probes each camera's RTSP reachability on a cadence and
-- reports per-camera status; cp-ingest records it here (#113). This is a
-- dedicated camera-status surface on the camera row — explicitly NOT a
-- device_health_probes row — so the device-page Cameras panel and the
-- camera_offline alert (#114) can speak in camera terms (label, id).
--
-- status is online | offline | unknown, defaulting to unknown so a
-- freshly-imported or just-added camera reads "unknown" rather than a
-- false online/offline before its first probe report lands.
-- last_checked_at / status_changed_at are NULL until the first report;
-- status_changed_at advances only when the status value actually
-- changes (see UpdateCameraStatus).
ALTER TABLE device_cameras
    ADD COLUMN status            text        NOT NULL DEFAULT 'unknown',
    ADD COLUMN last_checked_at   timestamptz,
    ADD COLUMN status_changed_at timestamptz;

-- +goose Down
ALTER TABLE device_cameras
    DROP COLUMN status,
    DROP COLUMN last_checked_at,
    DROP COLUMN status_changed_at;
