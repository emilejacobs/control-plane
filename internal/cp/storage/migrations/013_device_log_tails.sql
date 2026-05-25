-- +goose Up

-- Per-request rows for the Phase 2 slice 3 log-tail flow. One row per
-- operator-initiated tail request, keyed by correlation_id (the same
-- ID minted CP-side, sent down the cmd channel, and echoed by the
-- agent in cmd-result). Status walks pending → done | error as the
-- agent's ACK lands; content arrives in one shot (capped at ~200 KB
-- to fit MQTT) and stays in this row until the sweeper reaps it at
-- ~24h. See .scratch/phase-2-log-tail/PRD.md for the full design.
--
-- text column for status (not enum) so a future state can be added
-- without a migration — same posture as device_services.state.
CREATE TABLE device_log_tails (
    correlation_id  text        PRIMARY KEY,
    device_id       uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    log_name        text        NOT NULL,
    lines_requested integer     NOT NULL,
    status          text        NOT NULL,
    content         text,
    truncated       boolean     NOT NULL DEFAULT false,
    truncated_from  integer,
    error_code      text,
    error_message   text,
    requested_at    timestamptz NOT NULL,
    returned_at     timestamptz
);

-- Per-device, latest-first index for "show this device's tail history"
-- (a possible future surface; the dashboard doesn't need it yet but
-- the index is cheap and the alternative is a seq scan when it does).
CREATE INDEX device_log_tails_by_device
    ON device_log_tails (device_id, requested_at DESC);

-- Sweeper target. The 24h cleanup goroutine runs
--   DELETE FROM device_log_tails WHERE requested_at < now() - interval '24 hours'
-- — this index keeps that bounded.
CREATE INDEX device_log_tails_old ON device_log_tails (requested_at);

-- +goose Down

DROP TABLE device_log_tails;
