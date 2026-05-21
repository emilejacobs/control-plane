-- +goose Up
CREATE TABLE refresh_tokens (
    token_hash  bytea PRIMARY KEY,
    operator_id uuid NOT NULL REFERENCES operators(id) ON DELETE CASCADE,
    issued_at   timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz
);

CREATE INDEX refresh_tokens_operator_idx
    ON refresh_tokens(operator_id)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX refresh_tokens_operator_idx;
DROP TABLE refresh_tokens;
