# GitHub Actions OIDC role for the uknomi-edge-install pkg CI (#90, ADR-017/037).
#
# The uknomi-edge-install build-pkg workflow reads the bootstrap key at
# pkg-build time and bakes it into the signed .pkg. This grants it exactly that
# — GetSecretValue on the one bootstrap secret — via the GitHub OIDC provider
# already defined in ci-oidc.tf. It lives in this (stable, shared-infra) root
# rather than the per-device IoT root so applying it doesn't require a
# device_id. The bootstrap secret is created in the IoT root, so it's referenced
# here by name via a data source.

data "aws_secretsmanager_secret" "bootstrap_key" {
  name = "uknomi/cp/bootstrap-key"
}

data "aws_iam_policy_document" "edge_install_ci_trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    # The build-pkg workflow runs on main (workflow_dispatch). Widen the ref if
    # you dispatch the build from other branches.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:emilejacobs/uknomi-edge-install:ref:refs/heads/main"]
    }
  }
}

resource "aws_iam_role" "edge_install_ci" {
  name               = "uknomi-edge-install-ci-bootstrap-read"
  assume_role_policy = data.aws_iam_policy_document.edge_install_ci_trust.json
  tags               = { Name = "uknomi-edge-install-ci-bootstrap-read" }
}

data "aws_iam_policy_document" "edge_install_ci_read" {
  statement {
    sid       = "ReadBootstrapKey"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [data.aws_secretsmanager_secret.bootstrap_key.arn]
  }
}

resource "aws_iam_role_policy" "edge_install_ci_read" {
  name   = "read-bootstrap-key"
  role   = aws_iam_role.edge_install_ci.id
  policy = data.aws_iam_policy_document.edge_install_ci_read.json
}

output "edge_install_ci_role_arn" {
  description = "Role ARN the uknomi-edge-install build-pkg workflow assumes via OIDC to read the bootstrap key (set as the repo var AWS_BOOTSTRAP_ROLE_ARN)."
  value       = aws_iam_role.edge_install_ci.arn
}
