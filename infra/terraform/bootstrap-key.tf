# Bootstrap-key plumbing (ADR-017, Phase 1 issue 10).
#
# The static enrollment bootstrap key lives in AWS Secrets Manager. cp-api
# loads it at startup; the uknomi-edge-install CI reads it at pkg-build time and
# bakes it into the signed pkg (#90). The key itself never lands in Terraform
# state or in any repo's git history:
#
# - Terraform creates the secret *container* and seeds a non-secret
#   placeholder. The real key is set out-of-band with
#   `aws secretsmanager put-secret-value` and rotated the same way (~6-month
#   cadence, manual today). ignore_changes keeps Terraform from reverting it.
# - CI read access is the GitHub-OIDC role in edge-install-ci.tf, scoped to
#   GetSecretValue on this one secret ARN. (The legacy static-principal
#   bootstrap_ci role for the never-deployed mac-mini-rollout CI was retired
#   with #93.)

resource "aws_secretsmanager_secret" "bootstrap_key" {
  name        = "uknomi/cp/bootstrap-key"
  description = "Static enrollment bootstrap key (ADR-017). Set and rotated out-of-band; the uknomi-edge-install pkg is rebuilt to re-embed it."
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
