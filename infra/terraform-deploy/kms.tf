# Customer-managed KMS key for the Control Plane. Used by:
#   - Secrets Manager secrets in this root (jwt signing key, TOTP encryption
#     key, Tailscale auth key)
#   - RDS Postgres at-rest encryption (step 4)
#   - S3 buckets in step 12 (audit mirror, command output, agent dist)
#   - Phase 3's command-signing primitive
#
# One key for all of these for Phase 1 simplicity. A future security review
# may split by data class; the alias indirection means consumers keep
# working.

data "aws_caller_identity" "current" {}

resource "aws_kms_key" "main" {
  description             = "uknomi-cp customer-managed key (RDS, S3, Secrets Manager, command signing)"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  # Key policy is the access boundary for the key itself. IAM identity
  # principals (the account root) get full management; the AWS services
  # that consume the key get only the operations they need. Service
  # principals are scoped via the kms:ViaService condition so a stray
  # role with kms:Encrypt cannot exfiltrate through these services.
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "EnableIAMUserPermissions"
        Effect    = "Allow"
        Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
        Action    = "kms:*"
        Resource  = "*"
      },
      {
        Sid       = "AllowSecretsManagerUse"
        Effect    = "Allow"
        Principal = { Service = "secretsmanager.amazonaws.com" }
        Action = [
          "kms:Encrypt",
          "kms:Decrypt",
          "kms:ReEncrypt*",
          "kms:GenerateDataKey*",
          "kms:DescribeKey",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "kms:ViaService" = "secretsmanager.${var.region}.amazonaws.com"
          }
        }
      },
    ]
  })

  tags = { Name = "uknomi-cp" }
}

resource "aws_kms_alias" "main" {
  name          = "alias/uknomi-cp"
  target_key_id = aws_kms_key.main.key_id
}
