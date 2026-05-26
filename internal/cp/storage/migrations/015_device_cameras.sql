-- +goose Up

-- device_cameras stores the per-device cameras inventory. CP is the
-- source of truth (ADR-029 / ADR-030 § 1); the agent's local
-- cameras.json is a downstream copy pushed via the cameras.update
-- cmd. Camera IDs are assigned server-side in the form cam1, cam2,
-- ... per device.
--
-- The partial unique index enforces at-most-one is_lpr=true per
-- device (ADR-030 § 1) — Plate Recognizer config consumes the LPR
-- camera by querying for the row with is_lpr=true and assumes
-- single-truth.
CREATE TABLE device_cameras (
    device_id  uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    camera_id  text        NOT NULL,
    label      text        NOT NULL,
    rtsp_url   text        NOT NULL,
    is_lpr     boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, camera_id)
);

CREATE UNIQUE INDEX device_cameras_lpr_unique
    ON device_cameras (device_id) WHERE is_lpr = true;

-- +goose Down

DROP TABLE device_cameras;
