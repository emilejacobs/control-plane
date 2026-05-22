-- +goose Up
ALTER TABLE devices ADD COLUMN is_online boolean NOT NULL DEFAULT false;
ALTER TABLE devices ADD COLUMN presence_changed_at timestamptz;

-- +goose Down
ALTER TABLE devices DROP COLUMN presence_changed_at;
ALTER TABLE devices DROP COLUMN is_online;
