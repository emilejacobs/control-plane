-- +goose Up

-- Phase 2 fleet health probes (issue #19, ADR-019/ADR-034). One row per
-- (device, probe) carrying the agent-decided colour (status), the
-- OS-agnostic signal token (state), the structured per-probe payload
-- (details), and the cp-side ingest timestamp.
CREATE TABLE device_health_probes (
    device_id        uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    probe_name       text        NOT NULL,
    status           text        NOT NULL,
    state            text        NOT NULL,
    details          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    last_observed_at timestamptz NOT NULL,
    PRIMARY KEY (device_id, probe_name)
);

-- Partial index for the fleet-aggregate query ("which devices are red on
-- probe X right now?") and the per-probe-type CloudWatch alarm.
CREATE INDEX device_health_probes_red
    ON device_health_probes (probe_name)
    WHERE status = 'red';

-- +goose Down
DROP TABLE device_health_probes;
