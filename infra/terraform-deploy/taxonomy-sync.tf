# taxonomy-sync — daily Fargate task that mirrors the upstream
# clients/sites HTTP API into Postgres. ADR-033 + Issue #18.
#
# Same shape as audit-mirror (ADR-023): one-shot task on an
# EventBridge cron schedule, plus an ad-hoc RunTask path from cp-api
# (the "Force sync now" button). Failures bubble through a CloudWatch
# log-metric alarm; concurrency between the daily run and the manual
# button is gated by the binary's own pg_try_advisory_lock so no
# coordination at the infra layer is needed.

# ── Task role ──────────────────────────────────────────────────────────────
# Just Secrets Manager + KMS for the Cognito credentials. The task
# execution role (ecs.tf) covers image pull + CloudWatch Logs +
# fetching the DSN/credentials referenced in the task def `secrets`.

resource "aws_iam_role" "taxonomy_sync" {
  name               = "uknomi-cp-taxonomy-sync"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
  tags               = { Name = "uknomi-cp-taxonomy-sync" }
}

data "aws_iam_policy_document" "taxonomy_sync" {
  statement {
    sid       = "TaxonomyCredsRead"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.taxonomy_api_creds.arn]
  }
  statement {
    sid       = "DecryptForTaxonomyCreds"
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.main.arn]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["secretsmanager.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "taxonomy_sync" {
  name   = "taxonomy-sync-runtime"
  role   = aws_iam_role.taxonomy_sync.id
  policy = data.aws_iam_policy_document.taxonomy_sync.json
}

# ── Task definition ────────────────────────────────────────────────────────
# 256 CPU / 512 MB matches audit-mirror — taxonomy walks are short
# (one SignIn + ~1 brand + a handful of /brand/{id}/store responses)
# so the smallest Fargate size is more than enough.

resource "aws_ecs_task_definition" "taxonomy_sync" {
  family                   = "uknomi-cp-taxonomy-sync"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.taxonomy_sync.arn

  container_definitions = jsonencode([
    {
      name      = "taxonomy-sync"
      image     = "${aws_ecr_repository.main["taxonomy-sync"].repository_url}:${var.image_tag}"
      essential = true

      environment = [
        { name = "AWS_REGION", value = var.region },
        { name = "TAXONOMY_API_BASE_URL", value = "https://api.uknomi.com" },
      ]

      # Cognito service-account credentials live in the JSON secret
      # uknomi/cp/taxonomy-api-creds ({"username","password"}). ECS
      # task-def `secrets` resolves JSON fields via the SecretsManager
      # task-def syntax: ARN + ":<key>::".
      secrets = [
        { name = "DB_DSN", valueFrom = aws_secretsmanager_secret.db_dsn.arn },
        { name = "TAXONOMY_USERNAME", valueFrom = "${aws_secretsmanager_secret.taxonomy_api_creds.arn}:username::" },
        { name = "TAXONOMY_PASSWORD", valueFrom = "${aws_secretsmanager_secret.taxonomy_api_creds.arn}:password::" },
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.service["taxonomy-sync"].name
          awslogs-region        = var.region
          awslogs-stream-prefix = "taxonomy-sync"
        }
      }
    }
  ])

  tags = { Name = "uknomi-cp-taxonomy-sync" }
}

# ── EventBridge schedule ───────────────────────────────────────────────────
# Daily at 00:05 UTC — same minute audit-mirror runs, sequential
# tasks across that minute don't fight each other (different
# clusters of resources, low CPU).

resource "aws_cloudwatch_event_rule" "taxonomy_sync_daily" {
  name                = "uknomi-cp-taxonomy-sync-daily"
  description         = "Daily clients/sites taxonomy mirror sync (ADR-033)."
  schedule_expression = "cron(5 0 * * ? *)"
  tags                = { Name = "uknomi-cp-taxonomy-sync-daily" }
}

resource "aws_iam_role" "eventbridge_run_taxonomy_sync" {
  name               = "uknomi-cp-eventbridge-taxonomy-sync"
  assume_role_policy = data.aws_iam_policy_document.eventbridge_assume.json
  tags               = { Name = "uknomi-cp-eventbridge-taxonomy-sync" }
}

data "aws_iam_policy_document" "eventbridge_run_taxonomy_sync" {
  statement {
    sid       = "RunTask"
    actions   = ["ecs:RunTask"]
    resources = ["${aws_ecs_task_definition.taxonomy_sync.arn_without_revision}:*"]
    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [aws_ecs_cluster.main.arn]
    }
  }
  statement {
    sid       = "PassRoles"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.task_execution.arn, aws_iam_role.taxonomy_sync.arn]
  }
}

resource "aws_iam_role_policy" "eventbridge_run_taxonomy_sync" {
  name   = "eventbridge-run"
  role   = aws_iam_role.eventbridge_run_taxonomy_sync.id
  policy = data.aws_iam_policy_document.eventbridge_run_taxonomy_sync.json
}

resource "aws_cloudwatch_event_target" "taxonomy_sync" {
  rule     = aws_cloudwatch_event_rule.taxonomy_sync_daily.name
  arn      = aws_ecs_cluster.main.arn
  role_arn = aws_iam_role.eventbridge_run_taxonomy_sync.arn

  ecs_target {
    task_definition_arn = aws_ecs_task_definition.taxonomy_sync.arn
    launch_type         = "FARGATE"
    network_configuration {
      subnets          = aws_subnet.private[*].id
      security_groups  = [aws_security_group.tasks.id]
      assign_public_ip = false
    }
  }
}

# ── Alarms ─────────────────────────────────────────────────────────────────
# Two failure modes: explicit "failed" log line (a panic, an upstream
# error, an upsert error) and a stale-mirror condition (the
# "completed" line absent for >25 hours — schedule missed).

resource "aws_cloudwatch_log_metric_filter" "taxonomy_sync_failures" {
  name           = "uknomi-cp-taxonomy-sync-failures"
  log_group_name = aws_cloudwatch_log_group.service["taxonomy-sync"].name
  pattern        = "{ $.msg = \"taxonomy-sync failed\" }"

  metric_transformation {
    name          = "TaxonomySyncFailures"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "taxonomy_sync_failure" {
  alarm_name          = "uknomi-cp-taxonomy-sync-failure"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = aws_cloudwatch_log_metric_filter.taxonomy_sync_failures.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "taxonomy-sync task logged a failure. Runbook: docs/runbooks/alarms/taxonomy-sync.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-taxonomy-sync-failure" }
}

resource "aws_cloudwatch_log_metric_filter" "taxonomy_sync_completions" {
  name           = "uknomi-cp-taxonomy-sync-completions"
  log_group_name = aws_cloudwatch_log_group.service["taxonomy-sync"].name
  pattern        = "{ $.msg = \"taxonomy-sync completed\" }"

  metric_transformation {
    name          = "TaxonomySyncCompletions"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "taxonomy_sync_stale" {
  alarm_name          = "uknomi-cp-taxonomy-sync-stale"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 25
  metric_name         = aws_cloudwatch_log_metric_filter.taxonomy_sync_completions.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 3600
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "breaching"
  alarm_description   = "taxonomy-sync has not completed in the last 25 hours — the daily schedule may not be firing. Runbook: docs/runbooks/alarms/taxonomy-sync.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-taxonomy-sync-stale" }
}
