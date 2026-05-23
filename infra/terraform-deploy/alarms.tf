# Bare-minimum CloudWatch alarms per ADR-021. SNS topic with manual email
# subscriptions; the operator subscribes after first apply (`aws sns
# subscribe`). All alarms publish to the same topic; #21 layers on top.

resource "aws_sns_topic" "alarms" {
  name              = "uknomi-cp-alarms"
  kms_master_key_id = "alias/aws/sns"
  tags              = { Name = "uknomi-cp-alarms" }
}

# ── ALB ─────────────────────────────────────────────────────────────────────

resource "aws_cloudwatch_metric_alarm" "alb_5xx" {
  alarm_name          = "uknomi-cp-alb-5xx"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "HTTPCode_Target_5XX_Count"
  namespace           = "AWS/ApplicationELB"
  period              = 300
  statistic           = "Sum"
  threshold           = 5
  alarm_description   = "More than 5 target 5xx responses in 5 minutes on the CP ALB."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    LoadBalancer = aws_lb.main.arn_suffix
  }

  tags = { Name = "uknomi-cp-alb-5xx" }
}

# ── RDS ─────────────────────────────────────────────────────────────────────

resource "aws_cloudwatch_metric_alarm" "rds_cpu" {
  alarm_name          = "uknomi-cp-rds-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "CPUUtilization"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "RDS CPU above 80% for 10 minutes."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    DBInstanceIdentifier = aws_db_instance.main.identifier
  }

  tags = { Name = "uknomi-cp-rds-cpu" }
}

resource "aws_cloudwatch_metric_alarm" "rds_free_storage" {
  alarm_name          = "uknomi-cp-rds-free-storage"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 1
  metric_name         = "FreeStorageSpace"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 2 * 1024 * 1024 * 1024 # 2 GB
  alarm_description   = "RDS free storage below 2 GB."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    DBInstanceIdentifier = aws_db_instance.main.identifier
  }

  tags = { Name = "uknomi-cp-rds-free-storage" }
}

# ── Fargate services ────────────────────────────────────────────────────────
# Alarm when the running task count falls below desired for 5 minutes —
# catches crashloops and ENI exhaustion.

locals {
  ecs_alarmed_services = toset(["cp-api", "dashboard"]) # cp-ingest + tailscale start at desired=0
}

resource "aws_cloudwatch_metric_alarm" "service_running_low" {
  for_each            = local.ecs_alarmed_services
  alarm_name          = "uknomi-cp-${each.key}-running-count"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 1
  metric_name         = "RunningTaskCount"
  namespace           = "ECS/ContainerInsights"
  period              = 300
  statistic           = "Average"
  threshold           = 1
  alarm_description   = "${each.key} running task count below 1 for 5 minutes."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = each.key
  }

  tags = { Name = "uknomi-cp-${each.key}-running-count" }
}

# ── SQS DLQs ────────────────────────────────────────────────────────────────
# Any message in either DLQ is a defect — alarm immediately.

resource "aws_cloudwatch_metric_alarm" "heartbeat_dlq" {
  alarm_name          = "uknomi-cp-heartbeat-dlq"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "Heartbeat ingest DLQ is non-empty."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    QueueName = "uknomi-cp-heartbeat-dlq"
  }

  tags = { Name = "uknomi-cp-heartbeat-dlq" }
}

resource "aws_cloudwatch_metric_alarm" "lifecycle_dlq" {
  alarm_name          = "uknomi-cp-lifecycle-dlq"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "Lifecycle ingest DLQ is non-empty."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    QueueName = "uknomi-cp-lifecycle-dlq"
  }

  tags = { Name = "uknomi-cp-lifecycle-dlq" }
}
