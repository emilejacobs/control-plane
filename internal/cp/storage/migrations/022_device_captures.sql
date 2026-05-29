-- +goose Up

-- Phase 2 edge-UI rework (issue #8, ADR-030 § 7): the device→S3 binary-capture
-- index. One row per uploaded artifact (snapshot / audio / transcript); the
-- bytes live in S3 (bucket uknomi-cp-captures) and s3_key points at them.
-- kind is text + CHECK rather than a PG enum so a future kind is an additive
-- migration, not an ALTER TYPE (same rationale as device_services.state).
CREATE TABLE device_captures (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind         text        NOT NULL CHECK (kind IN ('snapshot', 'audio', 'transcript')),
    s3_key       text        NOT NULL,
    content_type text        NOT NULL,
    size_bytes   bigint      NOT NULL,
    metadata     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Serves the per-device, per-kind, newest-first list (GET
-- /devices/{id}/captures?kind=…) and the "latest snapshot" thumbnail lookup.
CREATE INDEX device_captures_by_device_kind
    ON device_captures (device_id, kind, created_at DESC);

-- +goose Down
DROP TABLE device_captures;
