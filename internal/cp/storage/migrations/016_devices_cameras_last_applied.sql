-- +goose Up

-- Mirror columns for the cameras.update ACK loop (Phase 2 Edge UI
-- rework, issue #2). Mirrors the slice-2 pattern from migration 012
-- (service_config_last_applied_*): CP writes the override locally on
-- the API call; cp-ingest's cmd-result handler stamps these columns
-- when the agent's ACK lands. The dashboard renders a "pending vs
-- applied" badge derived from the freshness of last_applied_at vs the
-- most-recent device_cameras updated_at.
ALTER TABLE devices
    ADD COLUMN cameras_last_applied_at      timestamptz,
    ADD COLUMN cameras_last_applied_corr_id text;

-- +goose Down

ALTER TABLE devices
    DROP COLUMN cameras_last_applied_at,
    DROP COLUMN cameras_last_applied_corr_id;
