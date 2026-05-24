-- +goose Up

-- Per-(device, service_name) state, fed by cp-ingest's ServiceStatusIngester
-- from the agent's devices/{id}/service-status MQTT reports (Phase 2,
-- ADR-018 worker pattern; see internal/protocol/servicestatus for the wire
-- type). The state column stays text (not enum) so Phase 3 can add the
-- 'failed' value without a migration once the agent's service.Backend
-- gains the ability to distinguish failed from intentionally-stopped.
CREATE TABLE device_services (
    device_id     uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    service_name  text        NOT NULL,
    state         text        NOT NULL,
    state_since   timestamptz NOT NULL,
    last_reported timestamptz NOT NULL,
    PRIMARY KEY (device_id, service_name)
);

-- Partial index for the Phase 2 alarm path: cp-ingest writes a
-- "service-status.stopped" log line on every stopped state seen; the
-- alarm's metric filter counts those, and the per-device API joins
-- against this table to render the dashboard's Services panel. The
-- partial-on-stopped form keeps the index small (most rows are running).
CREATE INDEX device_services_stopped
    ON device_services (device_id)
    WHERE state = 'stopped';

-- +goose Down

DROP TABLE device_services;
