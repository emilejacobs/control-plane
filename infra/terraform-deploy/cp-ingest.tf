# cp-ingest — two IoT-rule → SQS pipelines (heartbeat + lifecycle) and the
# Fargate service that drains them. Both halves come from existing modules
# next door in the IoT Core root (`infra/terraform/modules/`), so this file
# is mostly wiring.

# ── Heartbeat pipeline ──────────────────────────────────────────────────────

module "sqs_heartbeat" {
  source = "../terraform/modules/sqs-ingest"

  name          = "uknomi-cp-heartbeat"
  iot_rule_name = "uknomi_cp_heartbeat"
  iot_sql       = "SELECT *, topic(2) as device_id FROM 'devices/+/telemetry'"

  # Phase 1 message volume is small (25 devices × 30s heartbeat ≈ 50 msg/min);
  # the module defaults (4-day retention, 30s visibility, 5 redeliveries)
  # are fine.

  tags = var.tags
}

# ── Lifecycle pipeline ──────────────────────────────────────────────────────

module "sqs_lifecycle" {
  source = "../terraform/modules/sqs-ingest"

  name          = "uknomi-cp-lifecycle"
  iot_rule_name = "uknomi_cp_lifecycle"
  # AWS IoT lifecycle events arrive on $aws/events/presence/{eventType}/{thingName}.
  # The rule extracts thing-name + event-type as top-level columns so the
  # lifecycle handler can fan out without re-parsing the topic.
  #
  # newuuid() stamps each event with a correlation_id — AWS lifecycle events
  # do not carry one of their own, and sqsconsumer.Consumer[T] rejects
  # messages without one per ADR-011 (sending them to the DLQ). Omitting this
  # clause produced the lifecycle-DLQ growth surfaced in Wave 0.
  iot_sql = "SELECT *, eventType as event_type, clientId as device_id, newuuid() as correlation_id FROM '$aws/events/presence/+/+'"

  tags = var.tags
}

# ── Service-status pipeline (Phase 2, Issue 01) ────────────────────────────

module "sqs_service_status" {
  source = "../terraform/modules/sqs-ingest"

  name          = "uknomi-cp-service-status"
  iot_rule_name = "uknomi_cp_service_status"
  # Agent publishes its servicestatus.Report payload on
  # devices/{device_id}/service-status. The IoT Rule passes the body
  # through as-is; the agent already stamps a correlation_id and a
  # device_id in the JSON, so no topic-derived columns are needed.
  # Cadence is 5 minutes per ADR-018 throughput math
  # (~25 devices × 1 report / 5 min ≈ 5 msg/min). Visibility timeout
  # left at the module default (30s) — the per-service UPSERT loop is
  # cheap; the long-poll Wait + UPSERT roundtrip well inside that window.
  iot_sql = "SELECT * FROM 'devices/+/service-status'"

  tags = var.tags
}

# ── cp-ingest Fargate service ───────────────────────────────────────────────

module "cp_ingest" {
  source = "../terraform/modules/cp-ingest-service"

  image  = "${aws_ecr_repository.main["cp-ingest"].repository_url}:${var.image_tag}"
  region = var.region

  cluster_arn        = aws_ecs_cluster.main.arn
  subnet_ids         = aws_subnet.private[*].id
  security_group_ids = [aws_security_group.tasks.id]
  execution_role_arn = aws_iam_role.task_execution.arn
  task_role_arn      = aws_iam_role.cp_ingest.arn

  heartbeat_queue_url = module.sqs_heartbeat.queue_url
  heartbeat_dlq_url   = module.sqs_heartbeat.dlq_url
  lifecycle_queue_url = module.sqs_lifecycle.queue_url
  lifecycle_dlq_url   = module.sqs_lifecycle.dlq_url

  service_status_queue_url = module.sqs_service_status.queue_url
  service_status_dlq_url   = module.sqs_service_status.dlq_url

  db_dsn_secret_arn = aws_secretsmanager_secret.db_dsn.arn

  desired_count = 1

  tags = var.tags
}
