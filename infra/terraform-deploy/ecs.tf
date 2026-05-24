# ECS Fargate cluster + the task execution role every service uses (it is
# the role ECS itself assumes to pull images, write logs, and fetch
# Secrets Manager values referenced in task definitions). Per-service
# *task* roles for application-level AWS access live in task-roles.tf.

resource "aws_ecs_cluster" "main" {
  name = "uknomi-cp"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = { Name = "uknomi-cp" }
}

locals {
  # cp-ingest deliberately omitted — it runs under the standalone
  # infra/terraform/modules/cp-ingest-service module which manages its
  # own /uknomi/cp-ingest log group. A deploy-root group would just sit
  # empty (and earlier confused the sweeper-tick metric filter into
  # reading the wrong group; see git history for the fix).
  services = toset(["cp-api", "dashboard", "tailscale-subnet-router", "audit-mirror"])
}

resource "aws_cloudwatch_log_group" "service" {
  for_each = local.services

  name              = "/uknomi-cp/${each.key}"
  retention_in_days = 30
  tags              = { Name = "uknomi-cp-${each.key}" }
}

# ── Task execution role ─────────────────────────────────────────────────────

data "aws_iam_policy_document" "task_execution_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "task_execution" {
  name               = "uknomi-cp-task-execution"
  assume_role_policy = data.aws_iam_policy_document.task_execution_assume.json
  tags               = { Name = "uknomi-cp-task-execution" }
}

# AWS-managed: ECR pull + CloudWatch Logs write.
resource "aws_iam_role_policy_attachment" "task_execution_managed" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Secrets Manager access for the `secrets` attribute of task definitions:
# any uknomi/cp/* secret, plus the KMS Decrypt those secrets need (scoped
# via kms:ViaService so a stolen role token cannot use the key for
# anything but Secrets Manager).
data "aws_iam_policy_document" "task_execution_secrets" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = ["arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:uknomi/cp/*"]
  }
  statement {
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.main.arn]

    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["secretsmanager.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "task_execution_secrets" {
  name   = "secrets-manager-access"
  role   = aws_iam_role.task_execution.id
  policy = data.aws_iam_policy_document.task_execution_secrets.json
}
