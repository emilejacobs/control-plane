-- +goose Up

-- Agent fleet-update (issue #40, ADR-035 §1/§4): the per-device rollout
-- target. NULL = untargeted (no rollout has named this device). The rollout
-- model is entirely this column — no campaign entity; rollout state is
-- derived as desired-vs-reported (devices.agent_version), and the audit log
-- of "set desired" calls is the rollout record.
ALTER TABLE devices ADD COLUMN desired_agent_version text;

-- +goose Down
ALTER TABLE devices DROP COLUMN desired_agent_version;
