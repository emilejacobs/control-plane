-- +goose Up
-- The per-device mTLS cert notAfter, captured at enrollment. Nullable: the
-- column is additive and every post-006 enrollment populates it (Registry
-- writes cert.ExpiresAt), so null marks only pre-006 rows.
ALTER TABLE devices ADD COLUMN mtls_cert_expires_at timestamptz;

-- +goose Down
ALTER TABLE devices DROP COLUMN mtls_cert_expires_at;
