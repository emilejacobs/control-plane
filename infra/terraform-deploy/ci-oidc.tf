# GitHub Actions OIDC federation for the image-publish workflow (Issue 26).
#
# CI assumes this role via OIDC (no long-lived AWS keys) and pushes the
# three CP images to ECR on merge to main. The trust policy pins the OIDC
# sub claim to a single repo + branch so PR builds and forks cannot push.
#
# If the account already has a token.actions.githubusercontent.com OIDC
# provider, terraform apply will fail with EntityAlreadyExists; in that
# case `terraform import aws_iam_openid_connect_provider.github <arn>`.

resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
  tags            = { Name = "github-actions-oidc" }
}

data "aws_iam_policy_document" "gha_image_publish_trust" {
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
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repo}:ref:refs/heads/${var.github_image_publish_branch}"]
    }
  }
}

resource "aws_iam_role" "gha_image_publish" {
  name               = "uknomi-gha-image-publish"
  assume_role_policy = data.aws_iam_policy_document.gha_image_publish_trust.json
  tags               = { Name = "uknomi-gha-image-publish" }
}

data "aws_iam_policy_document" "gha_image_publish" {
  statement {
    sid       = "EcrAuth"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    sid = "EcrPushToCpRepos"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:CompleteLayerUpload",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
    ]
    resources = [for r in aws_ecr_repository.main : r.arn]
  }
}

resource "aws_iam_role_policy" "gha_image_publish" {
  name   = "image-publish"
  role   = aws_iam_role.gha_image_publish.id
  policy = data.aws_iam_policy_document.gha_image_publish.json
}
