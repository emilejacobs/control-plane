-- +goose Up

-- Agent fleet-update rollback visibility (#42 follow-up, ADR-035 §3). When the
-- resident wrapper health-gates a staged update and the candidate fails, it
-- reverts to last-known-good and records the reverted version. The agent reports
-- that version in its heartbeat; this column persists it so the rollout view can
-- distinguish a device that TRIED the desired version and rolled back from one
-- still in flight. NULL = no rollback reported. It is compared against
-- desired_agent_version at read time (a stale value for an older desired is
-- ignored once desired moves on or the device converges).
ALTER TABLE devices ADD COLUMN rolled_back_version text;

-- +goose Down
ALTER TABLE devices DROP COLUMN rolled_back_version;
