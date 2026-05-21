-- +goose Up
CREATE TABLE enrollment_idempotency (
    key             text PRIMARY KEY,
    status_code     integer NOT NULL,
    body            bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE enrollment_idempotency;
