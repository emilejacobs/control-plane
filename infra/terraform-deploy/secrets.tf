# Secrets Manager secrets for the Control Plane deployment.
#
# Each secret is created with a non-secret placeholder and `ignore_changes`
# on `secret_string` — Terraform owns the container, the operator owns the
# value (set out-of-band with `aws secretsmanager put-secret-value` once,
# rotated the same way). This is the pattern from #10's bootstrap key.
#
# The CP service that reads each secret:
#   - jwt_signing_key      → cp-api at startup (JWT_SIGNING_KEY env)
#   - totp_encryption_key  → cp-api at startup (TOTP_ENCRYPTION_KEY env)
#   - tailscale_auth_key   → tailscale-subnet-router task at startup
#
# The mac-mini-rollout install-package bootstrap key (#10) lives in the
# IoT Core root next door, for historical reasons; conceptually it
# belongs here, but moving it would require cross-root state migration
# and is not worth the churn today.

resource "aws_secretsmanager_secret" "jwt_signing_key" {
  name        = "uknomi/cp/jwt-signing-key"
  description = "HS256 signing key for cp-api JWTs (ADR-010). Base64 of at least 32 raw bytes. Set out-of-band; rotated manually."
  kms_key_id  = aws_kms_key.main.arn
}

resource "aws_secretsmanager_secret_version" "jwt_signing_key" {
  secret_id     = aws_secretsmanager_secret.jwt_signing_key.id
  secret_string = "PLACEHOLDER-set-the-real-key-out-of-band"

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "totp_encryption_key" {
  name        = "uknomi/cp/totp-encryption-key"
  description = "AES-256-GCM key for encrypting per-operator TOTP secrets at rest (ADR-010). Base64 of exactly 32 raw bytes."
  kms_key_id  = aws_kms_key.main.arn
}

resource "aws_secretsmanager_secret_version" "totp_encryption_key" {
  secret_id     = aws_secretsmanager_secret.totp_encryption_key.id
  secret_string = "PLACEHOLDER-set-the-real-key-out-of-band"

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "tailscale_auth_key" {
  name        = "uknomi/cp/tailscale-auth-key"
  description = "Non-expiring Tailscale auth key the subnet-router task uses to join the existing tailnet. Set out-of-band."
  kms_key_id  = aws_kms_key.main.arn
}

resource "aws_secretsmanager_secret_version" "tailscale_auth_key" {
  secret_id     = aws_secretsmanager_secret.tailscale_auth_key.id
  secret_string = "PLACEHOLDER-set-the-real-key-out-of-band"

  lifecycle {
    ignore_changes = [secret_string]
  }
}
