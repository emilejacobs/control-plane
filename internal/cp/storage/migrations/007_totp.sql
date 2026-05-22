-- +goose Up
-- Mandatory-TOTP columns on operators (Issue 05 / ADR-010). Both nullable:
-- the column is additive and an operator without a TOTP secret is forced
-- through enrollment by the first-login gate. totp_secret_encrypted holds
-- AES-256-GCM ciphertext; recovery_codes_hashed holds Argon2id PHC strings.
ALTER TABLE operators ADD COLUMN totp_secret_encrypted bytea;
ALTER TABLE operators ADD COLUMN recovery_codes_hashed text[];

-- +goose Down
ALTER TABLE operators DROP COLUMN recovery_codes_hashed;
ALTER TABLE operators DROP COLUMN totp_secret_encrypted;
