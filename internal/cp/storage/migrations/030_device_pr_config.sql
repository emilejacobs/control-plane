-- +goose Up

-- device_pr_config stores per-device Plate Recognizer settings that CP
-- manages (issue #5, ADR-030 § 3). CP is the source of truth for the
-- editable subset; the agent's pr.config.update handler MERGES these
-- fields into the existing on-disk config.ini (preserving hand-tuned
-- fields not modelled here — sample, detection_rule, dwell_time, ...).
--
-- One row per device (PK = device_id). The LPR camera RTSP URL is NOT
-- stored here — it is resolved at push time from device_cameras where
-- is_lpr=true (ADR-030 § 1), so the two surfaces can't drift.
--
-- enabled_webhooks is an inline [{name,url,enabled}] list for now; the
-- webhook registry (#6) will normalise it later. image defaults true
-- (issue #5 scope).
CREATE TABLE device_pr_config (
    device_id            uuid        NOT NULL PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    camera_id            text        NOT NULL DEFAULT '',
    region               text        NOT NULL DEFAULT '',
    caching              boolean     NOT NULL DEFAULT false,
    image                boolean     NOT NULL DEFAULT true,
    enabled_webhooks     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    last_applied_at      timestamptz,
    last_applied_corr_id text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE device_pr_config;
