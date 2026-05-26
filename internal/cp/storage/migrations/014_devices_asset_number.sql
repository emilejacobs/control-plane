-- +goose Up

-- asset_number is a fleet-tracking identifier assigned to each device
-- separately from the hostname (the leading "07-" in
-- "07-eegees-mesa-macmini" is NOT the asset number — that's something
-- else). NULL means "not yet assigned" — render as Unassigned on the
-- per-device Deployment card. Population path is install-module 11
-- shipping a value alongside the existing enrolment fields (Path A
-- per the open-work map); this migration only carves out the column.
ALTER TABLE devices
    ADD COLUMN asset_number text;

-- +goose Down

ALTER TABLE devices
    DROP COLUMN asset_number;
