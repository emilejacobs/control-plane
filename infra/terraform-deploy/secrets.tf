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

# The constructed Postgres DSN cp-api and cp-ingest read. Two paths to
# populate it post-RDS-apply, both manual (operator):
#   1. Read uknomi_admin's password from the RDS-managed secret, build
#      `postgresql://uknomi_admin:<pw>@<rds-endpoint>:5432/uknomi_cp?sslmode=require`,
#      and `aws secretsmanager put-secret-value` it here.
#   2. After any RDS master-password rotation, repeat step 1.
# A future improvement is to switch cp-api to read the username, password,
# and host as separate env vars (the password directly from the
# RDS-managed secret via ECS task-definition JSON-field references) — that
# eliminates this manual sync. Tracked as a follow-up.
resource "aws_secretsmanager_secret" "db_dsn" {
  name        = "uknomi/cp/db-dsn"
  description = "Constructed Postgres DSN for cp-api and cp-ingest. Set out-of-band; refresh after each RDS master-password rotation."
  kms_key_id  = aws_kms_key.main.arn
}

resource "aws_secretsmanager_secret_version" "db_dsn" {
  secret_id     = aws_secretsmanager_secret.db_dsn.id
  secret_string = "PLACEHOLDER-set-the-real-dsn-out-of-band"

  lifecycle {
    ignore_changes = [secret_string]
  }
}

# Cognito service-account credentials the cmd/taxonomy-sync task
# uses to call api.uknomi.com (ADR-033 § 7). The secret was created
# out-of-band on 2026-05-26 via AWS CLI and populated with real
# credentials by the user — import this resource before first apply:
#
#   terraform import aws_secretsmanager_secret.taxonomy_api_creds \
#     arn:aws:secretsmanager:us-east-1:523612763411:secret:uknomi/cp/taxonomy-api-creds-sPrbF6
#
# First apply re-encrypts the secret under aws_kms_key.main (the CP-
# managed key) — one-time, no data loss. `ignore_changes` on
# secret_string keeps Terraform from clobbering the real value with
# the placeholder.
resource "aws_secretsmanager_secret" "taxonomy_api_creds" {
  name        = "uknomi/cp/taxonomy-api-creds"
  description = "Cognito {username,password} for cmd/taxonomy-sync's SignIn against api.uknomi.com (ADR-033)."
  kms_key_id  = aws_kms_key.main.arn
}

resource "aws_secretsmanager_secret_version" "taxonomy_api_creds" {
  secret_id     = aws_secretsmanager_secret.taxonomy_api_creds.id
  secret_string = "{\"username\":\"REPLACE_ME\",\"password\":\"REPLACE_ME\"}"

  lifecycle {
    ignore_changes = [secret_string]
  }
}
