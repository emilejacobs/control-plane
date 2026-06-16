-- +goose Up

-- Per-device Plate Recognizer license (#84, ADR-036 §5). Plate Recognizer has
-- no key-minting API, so a staff operator enters the per-device license in the
-- CP; Commission pushes it to the device, which consumes it when it starts the
-- ALPR container (ADR-038). Secret: never returned raw by the API (the device
-- read surfaces only alpr_license IS NOT NULL) and never logged. Nullable —
-- NULL until staff sets it.
ALTER TABLE devices ADD COLUMN alpr_license text;

-- +goose Down
ALTER TABLE devices DROP COLUMN alpr_license;
