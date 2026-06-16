-- +goose Up

-- Fleet-notification alert state (PRD .scratch/fleet-notifications, #95). One
-- row per alert *occurrence*, keyed by alert identity (kind, device_id,
-- subject): kind ∈ {offline, service_stopped, probe_red}; subject is the
-- service/probe name ('' for offline). This is the dedupe boundary + the thing
-- that survives a cp-ingest restart — the reconciler diffs the live
-- fleet-unhealthy snapshot against the OPEN rows here to fire transition-only
-- notifications (the in-memory presence model can't, it's ephemeral).
--
-- An alert is "open" iff resolved_at IS NULL. last_notified_at is NULL until a
-- digest carrying it has actually been delivered, so a failed send leaves the
-- row un-notified and the next tick retries (at-least-once). Resolved rows are
-- retained as history: a signal that flaps re-opens a fresh row rather than
-- mutating a closed one.
CREATE TABLE alert_state (
    kind             text        NOT NULL,
    device_id        uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    subject          text        NOT NULL DEFAULT '',
    opened_at        timestamptz NOT NULL,
    last_notified_at timestamptz,
    notify_attempts  integer     NOT NULL DEFAULT 0,
    resolved_at      timestamptz
);

-- At most one OPEN row per alert identity. Enforces the dedupe ("still
-- unhealthy → no new row") at the DB and lets OpenAlert be an idempotent
-- ON CONFLICT DO NOTHING. Resolved rows (resolved_at NOT NULL) are excluded,
-- so history accumulates without tripping the constraint.
CREATE UNIQUE INDEX alert_state_open_identity
    ON alert_state (kind, device_id, subject)
    WHERE resolved_at IS NULL;

-- +goose Down
DROP TABLE alert_state;
