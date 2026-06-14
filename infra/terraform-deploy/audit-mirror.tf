# audit-mirror — daily Fargate task that exports the prior UTC day's
# audit_log rows to s3://<audit-mirror>/<YYYY>/<MM>/<DD>.jsonl.gz.
# ADR-023 + Issue 28.
#
# The schedule is EventBridge → ECS RunTask (00:05 UTC daily). The task
# is one-shot: it exits as soon as ExportDate returns. Failures bubble
# back through the task's exit code; the CloudWatch alarms (below) page
# on either an explicit non-zero exit or a missed schedule.

# ── Task role ───────────────────────────────────────────────────────────────
# Scoped to s3:PutObject + s3:GetObject + s3:HeadObject on the
# audit-mirror bucket only. No SecretsManager:* on anything else; no S3 on
# the other buckets. The task execution role (from ecs.tf) already covers
# image pull + log write + the DB password fetch (from the RDS-managed
# secret) via task-def secrets (issue #49).

resource "aws_iam_role" "audit_mirror" {
  name               = "uknomi-cp-audit-mirror"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
  tags               = { Name = "uknomi-cp-audit-mirror" }
}

data "aws_iam_policy_document" "audit_mirror" {
  statement {
    sid = "S3MirrorWrite"
    actions = [
      "s3:PutObject",
      "s3:GetObject",
      # HeadObject in the SDK maps to s3:GetObject; included for clarity.
    ]
    resources = [
      "${aws_s3_bucket.main["audit-mirror"].arn}/*",
    ]
  }
  statement {
    sid       = "S3MirrorList"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.main["audit-mirror"].arn]
  }
}

resource "aws_iam_role_policy" "audit_mirror" {
  name   = "audit-mirror-runtime"
  role   = aws_iam_role.audit_mirror.id
  policy = data.aws_iam_policy_document.audit_mirror.json
}

# ── Task definition ─────────────────────────────────────────────────────────

resource "aws_ecs_task_definition" "audit_mirror" {
  family                   = "uknomi-cp-audit-mirror"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.audit_mirror.arn

  container_definitions = jsonencode([
    {
      name      = "audit-mirror"
      image     = "${aws_ecr_repository.main["audit-mirror"].repository_url}:${var.image_tag}"
      essential = true

      environment = concat([
        { name = "AUDIT_BUCKET", value = aws_s3_bucket.main["audit-mirror"].bucket },
        { name = "AWS_REGION", value = var.region },
      ], local.db_environment)

      secrets = [
        local.db_password_secret,
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.service["audit-mirror"].name
          awslogs-region        = var.region
          awslogs-stream-prefix = "audit-mirror"
        }
      }
    }
  ])

  tags = { Name = "uknomi-cp-audit-mirror" }
}

# ── EventBridge schedule ────────────────────────────────────────────────────
# Daily at 00:05 UTC. Picked 5 minutes after midnight so the previous
# day's last lifecycle/login traffic has settled into audit_log.

resource "aws_cloudwatch_event_rule" "audit_mirror_daily" {
  name                = "uknomi-cp-audit-mirror-daily"
  description         = "Daily audit-log S3 mirror — exports the prior UTC day."
  schedule_expression = "cron(5 0 * * ? *)"
  tags                = { Name = "uknomi-cp-audit-mirror-daily" }
}

# EventBridge needs its own IAM role to call ECS RunTask + PassRole on
# the task execution + task roles.
data "aws_iam_policy_document" "eventbridge_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "eventbridge_run_audit_mirror" {
  name               = "uknomi-cp-eventbridge-audit-mirror"
  assume_role_policy = data.aws_iam_policy_document.eventbridge_assume.json
  tags               = { Name = "uknomi-cp-eventbridge-audit-mirror" }
}

data "aws_iam_policy_document" "eventbridge_run_audit_mirror" {
  statement {
    sid       = "RunTask"
    actions   = ["ecs:RunTask"]
    resources = ["${aws_ecs_task_definition.audit_mirror.arn_without_revision}:*"]
    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [aws_ecs_cluster.main.arn]
    }
  }
  statement {
    sid       = "PassRoles"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.task_execution.arn, aws_iam_role.audit_mirror.arn]
  }
}

resource "aws_iam_role_policy" "eventbridge_run_audit_mirror" {
  name   = "eventbridge-run"
  role   = aws_iam_role.eventbridge_run_audit_mirror.id
  policy = data.aws_iam_policy_document.eventbridge_run_audit_mirror.json
}

resource "aws_cloudwatch_event_target" "audit_mirror" {
  rule     = aws_cloudwatch_event_rule.audit_mirror_daily.name
  arn      = aws_ecs_cluster.main.arn
  role_arn = aws_iam_role.eventbridge_run_audit_mirror.arn

  ecs_target {
    task_definition_arn = aws_ecs_task_definition.audit_mirror.arn
    launch_type         = "FARGATE"
    network_configuration {
      subnets          = aws_subnet.private[*].id
      security_groups  = [aws_security_group.tasks.id]
      assign_public_ip = false
    }
  }
}

# ── Alarms ─────────────────────────────────────────────────────────────────
# Two failure modes worth paging on: (a) the task ran but exited non-zero,
# (b) the schedule did not fire at all in the past 25 hours.

# Stopped-task alarm: any ECS "essential container exited non-zero"
# in the audit-mirror log group is a failure. The exporter's "completed"
# log line is the success signal — its absence over 25h is what
# alarm (b) below catches.
resource "aws_cloudwatch_log_metric_filter" "audit_mirror_failures" {
  name           = "uknomi-cp-audit-mirror-failures"
  log_group_name = aws_cloudwatch_log_group.service["audit-mirror"].name
  pattern        = "{ $.msg = \"audit-mirror failed\" }"

  metric_transformation {
    name          = "AuditMirrorFailures"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "audit_mirror_failure" {
  alarm_name          = "uknomi-cp-audit-mirror-failure"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = aws_cloudwatch_log_metric_filter.audit_mirror_failures.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "audit-mirror task logged a failure. Runbook: docs/runbooks/alarms/audit-mirror.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-audit-mirror-failure" }
}

# Stale-mirror alarm: success line absent for 25h. The task runs every
# 24h; 25h gives 1h of slack for a delayed start.
resource "aws_cloudwatch_log_metric_filter" "audit_mirror_completions" {
  name           = "uknomi-cp-audit-mirror-completions"
  log_group_name = aws_cloudwatch_log_group.service["audit-mirror"].name
  pattern        = "{ $.msg = \"audit-mirror completed\" }"

  metric_transformation {
    name          = "AuditMirrorCompletions"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "audit_mirror_stale" {
  alarm_name          = "uknomi-cp-audit-mirror-stale"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 25
  metric_name         = aws_cloudwatch_log_metric_filter.audit_mirror_completions.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 3600
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "breaching"
  alarm_description   = "audit-mirror has not completed in the last 25 hours — the daily schedule may not be firing. Runbook: docs/runbooks/alarms/audit-mirror.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-audit-mirror-stale" }
}
