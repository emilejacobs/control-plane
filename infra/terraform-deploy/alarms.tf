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

# Phase 2 slice 1 service-status DLQ + slice 2 cmd-result DLQ. Both
# should be empty in steady state; any message means a payload the
# ingester couldn't process (unknown device, schema drift, validation
# bug). Same posture as heartbeat/lifecycle alarms above.
resource "aws_cloudwatch_metric_alarm" "service_status_dlq" {
  alarm_name          = "uknomi-cp-service-status-dlq"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "Service-status ingest DLQ is non-empty (Phase 2 slice 1 pipeline)."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    QueueName = "uknomi-cp-service-status-dlq"
  }

  tags = { Name = "uknomi-cp-service-status-dlq" }
}

resource "aws_cloudwatch_metric_alarm" "cmd_result_dlq" {
  alarm_name          = "uknomi-cp-cmd-result-dlq"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "Cmd-result ingest DLQ is non-empty (Phase 2 slice 2 config.update + slice 3 log.tail ACK pipeline)."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    QueueName = "uknomi-cp-cmd-result-dlq"
  }

  tags = { Name = "uknomi-cp-cmd-result-dlq" }
}

# Phase 2 issue #19 health-probes DLQ. Non-empty means the ingester
# rejected a probe report (unknown device, schema drift). Same posture
# as the service-status DLQ alarm.
resource "aws_cloudwatch_metric_alarm" "health_probes_dlq" {
  alarm_name          = "uknomi-cp-health-probes-dlq"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "Health-probes ingest DLQ is non-empty (Phase 2 issue #19 pipeline). Runbook: docs/runbooks/alarms/health-probes-dlq.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    QueueName = "uknomi-cp-health-probes-dlq"
  }

  tags = { Name = "uknomi-cp-health-probes-dlq" }
}

resource "aws_cloudwatch_metric_alarm" "camera_status_dlq" {
  alarm_name          = "uknomi-cp-camera-status-dlq"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "Camera-status ingest DLQ is non-empty (#113 camera observability pipeline)."
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  dimensions = {
    QueueName = "uknomi-cp-camera-status-dlq"
  }

  tags = { Name = "uknomi-cp-camera-status-dlq" }
}

# ── Log-derived alarms (Issue 21) ───────────────────────────────────────────
# Each pairs an aws_cloudwatch_log_metric_filter (JSON-pattern over the
# service's structured slog stream) with an aws_cloudwatch_metric_alarm.
# default_value = 0 on each filter so the metric reports a real zero in
# quiet periods — without it, "no matches" reads as "no data" and the
# absence-detection (sweeper lag) cannot fire.
#
# Runbooks: docs/runbooks/alarms/<alarm-name>.md.

locals {
  cp_audit_namespace = "Uknomi/CP/Audit"
}

# Sweeper lag — paged when the cp-ingest sweeper goroutine has not ticked
# in the last 60s. The ingest module's sweeper logs "sweeper.tick" each
# pass; the alarm reads the metric as the gap signal.

resource "aws_cloudwatch_log_metric_filter" "sweeper_tick" {
  name = "uknomi-cp-sweeper-tick"
  # cp-ingest is wired through the standalone module under
  # infra/terraform/modules/cp-ingest-service, which manages its own log
  # group (/uknomi/cp-ingest). The deploy root does not create a
  # service["cp-ingest"] entry (see local.services in ecs.tf), so the
  # filter has to read the module's output to land in the right group.
  log_group_name = module.cp_ingest.log_group_name
  pattern        = "{ $.msg = \"sweeper.tick\" }"

  metric_transformation {
    name          = "SweeperTicks"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "sweeper_lag" {
  alarm_name          = "uknomi-cp-sweeper-lag"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 2
  metric_name         = aws_cloudwatch_log_metric_filter.sweeper_tick.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 60
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "breaching"
  alarm_description   = "PresenceSweeper has not ticked in the last 2 minutes — a stuck goroutine fails the lifecycle backstop. Runbook: docs/runbooks/alarms/sweeper-lag.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-sweeper-lag" }
}

# Service-status stopped — Phase 2 (Issue 01). Counts the
# "service-status.stopped" lines cp-ingest's ServiceStatusIngester
# emits, one per stopped service per report. Slice 1 uses 'stopped'
# (the only "not running" state the agent's launchctl/systemctl
# backend can reliably emit today; the PRD § Refinements explains the
# false-positive risk on operator-initiated stops).
resource "aws_cloudwatch_log_metric_filter" "service_status_stopped" {
  name           = "uknomi-cp-service-status-stopped"
  log_group_name = module.cp_ingest.log_group_name
  pattern        = "{ $.msg = \"service-status.stopped\" }"

  metric_transformation {
    name          = "ServiceStatusStopped"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "service_status_stopped" {
  alarm_name          = "uknomi-cp-service-stopped"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3 # require ≥3 consecutive 5-min windows = 15 min
  metric_name         = aws_cloudwatch_log_metric_filter.service_status_stopped.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "An allow-listed service has been in the stopped state for ≥15 minutes (3 consecutive 5-min reports). Runbook: docs/runbooks/alarms/service-stopped.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-service-stopped" }
}

# Health-probe red — Phase 2 (Issue #19), per-probe-type. The triage
# decision is one alarm per probe type rather than a single roll-up, so
# the page tells the operator which signal is red. cp-ingest's
# HealthProbeIngester emits one "health-probe.red" line per red probe
# per report, stamped with the probe name in $.probe; one filter+alarm
# pair per probe type counts the matching lines. ≥3 consecutive 5-min
# windows = the probe has been red on ≥1 device for ≥15 min.
locals {
  health_probe_names = [
    "auto_login",
    "gui_session",
    "plate_recognizer_container",
    "plate_recognizer_config",
    "usb_audio",
    "whisper_model",
    "boot_sanity",
  ]
}

resource "aws_cloudwatch_log_metric_filter" "health_probe_red" {
  for_each       = toset(local.health_probe_names)
  name           = "uknomi-cp-health-probe-red-${each.key}"
  log_group_name = module.cp_ingest.log_group_name
  pattern        = "{ $.msg = \"health-probe.red\" && $.probe = \"${each.key}\" }"

  metric_transformation {
    name          = "HealthProbeRed_${each.key}"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "health_probe_red" {
  for_each            = toset(local.health_probe_names)
  alarm_name          = "uknomi-cp-health-probe-${each.key}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3 # ≥3 consecutive 5-min windows = 15 min
  metric_name         = aws_cloudwatch_log_metric_filter.health_probe_red[each.key].metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "Health probe ${each.key} has been red on ≥1 device for ≥15 minutes. Runbook: docs/runbooks/alarms/health-probe-red.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-health-probe-${each.key}" }
}

# Login failure spike — paged when /auth/login failure lines breach 100
# in a 5-minute window. ADR-017 hardens against bursts; the alarm calls
# it out before lockout thresholds quietly absorb a brute-force attempt.

resource "aws_cloudwatch_log_metric_filter" "login_failures" {
  name           = "uknomi-cp-login-failures"
  log_group_name = aws_cloudwatch_log_group.service["cp-api"].name
  pattern        = "{ $.msg = \"audit.login\" && $.outcome = \"failure\" }"

  metric_transformation {
    name          = "LoginFailures"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "login_failure_spike" {
  alarm_name          = "uknomi-cp-login-failure-spike"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = aws_cloudwatch_log_metric_filter.login_failures.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 100
  treat_missing_data  = "notBreaching"
  alarm_description   = "More than 100 /auth/login failures in 5 minutes. Investigate via CloudWatch Insights query by email + source_ip. Runbook: docs/runbooks/alarms/login-failure-spike.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-login-failure-spike" }
}

# Enrollment rate-limit trip — any IP probing /enrollments past its
# fixed-window cap pages immediately. The aggregate-count alarm flags
# "something is probing"; per-IP attribution is the runbook's Insights
# query against the ratelimit.trip lines (CloudWatch metric filters
# cannot cleanly express per-dimension grouping).

resource "aws_cloudwatch_log_metric_filter" "ratelimit_trips" {
  name           = "uknomi-cp-ratelimit-trips"
  log_group_name = aws_cloudwatch_log_group.service["cp-api"].name
  pattern        = "{ $.msg = \"ratelimit.trip\" }"

  metric_transformation {
    name          = "RatelimitTrips"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "enrollment_ratelimit_trip" {
  alarm_name          = "uknomi-cp-enrollment-ratelimit-trip"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = aws_cloudwatch_log_metric_filter.ratelimit_trips.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "An IP tripped the /enrollments per-IP rate limit. Runbook: docs/runbooks/alarms/enrollment-ratelimit-trip.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-enrollment-ratelimit-trip" }
}

# Hostname-convention anomaly — every audit.enrollment.anomaly line is a
# sanity-check miss, not an attack; we still page because in Phase 1 the
# fleet's hostname convention is enforced socially, not by the API, and
# a typo'd enrollment is worth catching while the install script is
# still on someone's terminal.

resource "aws_cloudwatch_log_metric_filter" "hostname_anomalies" {
  name           = "uknomi-cp-hostname-anomalies"
  log_group_name = aws_cloudwatch_log_group.service["cp-api"].name
  pattern        = "{ $.msg = \"audit.enrollment.anomaly\" }"

  metric_transformation {
    name          = "HostnameAnomalies"
    namespace     = local.cp_audit_namespace
    value         = "1"
    default_value = "0"
    unit          = "Count"
  }
}

resource "aws_cloudwatch_metric_alarm" "hostname_anomaly" {
  alarm_name          = "uknomi-cp-hostname-anomaly"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = aws_cloudwatch_log_metric_filter.hostname_anomalies.metric_transformation[0].name
  namespace           = local.cp_audit_namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  alarm_description   = "A device enrolled with a hostname off the project convention. Runbook: docs/runbooks/alarms/hostname-anomaly.md"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]

  tags = { Name = "uknomi-cp-hostname-anomaly" }
}
