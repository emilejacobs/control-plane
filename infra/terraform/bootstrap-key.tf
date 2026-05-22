# Bootstrap-key plumbing (ADR-017, Phase 1 issue 10).
#
# The static enrollment bootstrap key lives in AWS Secrets Manager. cp-api
# loads it at startup; the mac-mini-rollout CI reads it at install-package
# build time and bakes it into the package. The key itself never lands in
# Terraform state or in either repo's git history:
#
# - Terraform creates the secret *container* and seeds a non-secret
#   placeholder. The real key is set out-of-band with
#   `aws secretsmanager put-secret-value` and rotated the same way (~6-month
#   cadence, manual today). ignore_changes keeps Terraform from reverting it.
# - The CI role is scoped to GetSecretValue on this one secret ARN — nothing
#   else.

resource "aws_secretsmanager_secret" "bootstrap_key" {
  name        = "uknomi/cp/bootstrap-key"
  description = "Static enrollment bootstrap key (ADR-017). Set and rotated out-of-band; the mac-mini-rollout install package is rebuilt to re-embed it."
}

resource "aws_secretsmanager_secret_version" "bootstrap_key" {
  secret_id     = aws_secretsmanager_secret.bootstrap_key.id
  secret_string = "PLACEHOLDER-set-the-real-key-out-of-band"

  # The real key is managed out-of-band; Terraform must neither revert it
  # nor pull it into state on a later apply.
  lifecycle {
    ignore_changes = [secret_string]
  }
}

# IAM role the mac-mini-rollout CI assumes to read the bootstrap key at
# install-package build time. Scoped to exactly one action on one secret.
resource "aws_iam_role" "bootstrap_ci" {
  name        = "uknomi-cp-bootstrap-key-ci"
  description = "Assumed by the mac-mini-rollout CI to read the bootstrap key at build time (ADR-017)."

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { AWS = var.bootstrap_ci_principal_arns }
    }]
  })
}

resource "aws_iam_role_policy" "bootstrap_ci_read" {
  name = "read-bootstrap-key"
  role = aws_iam_role.bootstrap_ci.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "secretsmanager:GetSecretValue"
      Resource = aws_secretsmanager_secret.bootstrap_key.arn
    }]
  })
}
