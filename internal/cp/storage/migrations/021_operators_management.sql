-- +goose Up

-- Issue #16 operator management. Two columns on operators:
--   deactivated_at       — soft delete. A non-null value means the operator
--                          can no longer authenticate, but the row (and the
--                          audit-log references to it) is preserved.
--                          Reactivation clears it back to NULL.
--   must_change_password — set when a staff admin creates an operator (or
--                          resets its password) with a system-generated temp
--                          password. While true the operator must set a new
--                          password before any normal action; cleared once
--                          they do.
ALTER TABLE operators
    ADD COLUMN deactivated_at       timestamptz,
    ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE operators
    DROP COLUMN deactivated_at,
    DROP COLUMN must_change_password;
