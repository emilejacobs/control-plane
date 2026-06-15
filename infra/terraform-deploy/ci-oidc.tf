# GitHub Actions OIDC federation for the CI deploy workflow.
#
# Originally provisioned by Issue 26 (image push only); widened by
# ADR-027 (Phase 1 auto-deploy direct to prod) to also roll the ECS
# services after each image push. The role name stays `uknomi-gha-image-publish`
# for continuity with the workflow's role-to-assume ARN — the `deploy`
# inline policy below is what actually grants the rollout perms.
#
# CI assumes this role via OIDC (no long-lived AWS keys): build-images on
# merge to main, and agent-release on an `agent-v*` tag push (issue #39 — the
# release workflow uploads binaries + the signed manifest to agent-dist). The
# trust policy pins the OIDC sub claim to this repo's main branch + release
# tags, so PR builds and forks cannot push or deploy.
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
    # StringLike so the value list is OR'd: the main branch (exact — no
    # wildcard) for build-images, plus agent-v* tags for agent-release.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values = [
        "repo:${var.github_repo}:ref:refs/heads/${var.github_image_publish_branch}",
        "repo:${var.github_repo}:ref:refs/tags/agent-v*",
      ]
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

# ── Deploy permissions (ADR-027) ────────────────────────────────────────────
# The same OIDC role used to push images also rolls the ECS services after
# each push. Scoped tightly: UpdateService/DescribeServices only on the
# three long-running CP services; RunTask only on the audit-mirror task
# def; PassRole only on the task execution + audit-mirror task roles
# (the two roles RunTask hands to the new task). ListTargetsByRule on
# the audit-mirror EventBridge rule lets the workflow re-derive the
# task's network config at runtime rather than hardcoding subnet/SG IDs.

data "aws_iam_policy_document" "gha_deploy" {
  statement {
    sid = "UpdateAndDescribeCpServices"
    actions = [
      "ecs:UpdateService",
      "ecs:DescribeServices",
    ]
    resources = [
      "arn:aws:ecs:${var.region}:${data.aws_caller_identity.current.account_id}:service/${aws_ecs_cluster.main.name}/cp-api",
      "arn:aws:ecs:${var.region}:${data.aws_caller_identity.current.account_id}:service/${aws_ecs_cluster.main.name}/cp-ingest",
      "arn:aws:ecs:${var.region}:${data.aws_caller_identity.current.account_id}:service/${aws_ecs_cluster.main.name}/dashboard",
    ]
  }

  statement {
    sid     = "RunScheduledTasks"
    actions = ["ecs:RunTask"]
    resources = [
      "${aws_ecs_task_definition.audit_mirror.arn_without_revision}:*",
      "${aws_ecs_task_definition.taxonomy_sync.arn_without_revision}:*",
    ]
    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [aws_ecs_cluster.main.arn]
    }
  }

  # DescribeTasks is needed to surface a failed run-task in the workflow
  # output. ResourceTag condition scopes it to tasks in our cluster.
  statement {
    sid       = "DescribeTasksInCpCluster"
    actions   = ["ecs:DescribeTasks"]
    resources = ["arn:aws:ecs:${var.region}:${data.aws_caller_identity.current.account_id}:task/${aws_ecs_cluster.main.name}/*"]
  }

  statement {
    sid     = "PassRolesToScheduledTasks"
    actions = ["iam:PassRole"]
    resources = [
      aws_iam_role.task_execution.arn,
      aws_iam_role.audit_mirror.arn,
      aws_iam_role.taxonomy_sync.arn,
    ]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }

  statement {
    sid     = "ReadScheduledRuleTargets"
    actions = ["events:ListTargetsByRule"]
    resources = [
      aws_cloudwatch_event_rule.audit_mirror_daily.arn,
      aws_cloudwatch_event_rule.taxonomy_sync_daily.arn,
    ]
  }

  # Agent release (#38): the agent-release workflow uploads cross-built agent
  # binaries + the signed manifest under the agent-dist bucket's agent/ prefix.
  statement {
    sid       = "PublishAgentReleaseArtifacts"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.main["agent-dist"].arn}/agent/*"]
  }
}

resource "aws_iam_role_policy" "gha_deploy" {
  name   = "deploy"
  role   = aws_iam_role.gha_image_publish.id
  policy = data.aws_iam_policy_document.gha_deploy.json
}
