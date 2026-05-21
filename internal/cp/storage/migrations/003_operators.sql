-- +goose Up
CREATE TABLE operators (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email              text NOT NULL UNIQUE,
    password_hash      text NOT NULL,
    is_staff           boolean NOT NULL DEFAULT true,
    failed_login_count int NOT NULL DEFAULT 0,
    locked_until       timestamptz,
    last_login_at      timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE operators;
