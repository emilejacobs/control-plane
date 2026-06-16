# GitHub Actions OIDC role for the uknomi-edge-install CI (#90, ADR-017/037).
#
# The uknomi-edge-install build-pkg workflow reads the bootstrap key at
# pkg-build time and bakes it into the signed .pkg. This grants it exactly that
# — GetSecretValue on the one bootstrap secret — via GitHub OIDC (no long-lived
# keys), reusing the OIDC provider already created in terraform-deploy.
#
# Apply this root manually (ADR-027). Then point the workflow's
# aws-actions/configure-aws-credentials at the output role ARN.

data "aws_iam_openid_connect_provider" "github" {
  url = "https://token.actions.githubusercontent.com"
}

resource "aws_iam_role" "edge_install_ci" {
  name        = "uknomi-edge-install-ci-bootstrap-read"
  description = "Assumed by the uknomi-edge-install CI (GitHub OIDC) to read the bootstrap key at pkg-build time (ADR-017)."

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = data.aws_iam_openid_connect_provider.github.arn }
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        }
        # Restricted to the build-pkg workflow running on main. Widen the ref
        # if you dispatch the build from other branches.
        StringLike = {
          "token.actions.githubusercontent.com:sub" = "repo:emilejacobs/uknomi-edge-install:ref:refs/heads/main"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy" "edge_install_ci_read" {
  name = "read-bootstrap-key"
  role = aws_iam_role.edge_install_ci.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "secretsmanager:GetSecretValue"
      Resource = aws_secretsmanager_secret.bootstrap_key.arn
    }]
  })
}

output "edge_install_ci_role_arn" {
  description = "Role ARN the uknomi-edge-install build-pkg workflow assumes via OIDC to read the bootstrap key."
  value       = aws_iam_role.edge_install_ci.arn
}
