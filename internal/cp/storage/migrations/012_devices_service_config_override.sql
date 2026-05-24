-- +goose Up

-- Phase 2 slice 2 per-device override of the agent's service allow-list
-- and reporting cadence. See .scratch/phase-2-allow-list-overrides/PRD.md
-- for the override semantics: nil = no override (agent uses its bundled
-- list); JSONB array = effective list; '[]' = "track nothing" (distinct
-- from nil, persisted literally).
--
-- One JSONB column today (one knob); promote to a device_config table
-- only when a second knob arrives and joins matter — per ADR-019 we
-- prefer additive migrations over premature normalisation.
--
-- last_applied_* fields are written by the cp-ingest cmd-result handler
-- when a config.update ACK lands; the dashboard reads them to render
-- "pending" vs "applied" badges on the Services panel.
ALTER TABLE devices
    ADD COLUMN service_allow_list_override         jsonb,
    ADD COLUMN service_status_interval_override    text,
    ADD COLUMN service_config_last_applied_at      timestamptz,
    ADD COLUMN service_config_last_applied_corr_id text;

-- +goose Down

ALTER TABLE devices
    DROP COLUMN service_allow_list_override,
    DROP COLUMN service_status_interval_override,
    DROP COLUMN service_config_last_applied_at,
    DROP COLUMN service_config_last_applied_corr_id;
