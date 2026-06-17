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

# ── Cmd-result pipeline (Phase 2 slice 2, allow-list overrides) ────────────

module "sqs_cmd_result" {
  source = "../terraform/modules/sqs-ingest"

  name          = "uknomi-cp-cmd-result"
  iot_rule_name = "uknomi_cp_cmd_result"
  # Agent publishes envelope.Result JSON on devices/{device_id}/cmd-result
  # after handling any cmd. The IoT Rule injects device_id from topic(2)
  # since the on-wire envelope doesn't natively carry it (the topic does).
  # cp-ingest's CmdResultIngester routes by Type field — slice 2 only
  # handles "config.update"; other types are silently ignored so Phase 3
  # can add per-type handlers later without breaking flow.
  #
  # Cadence is operator-driven (one cmd-result per PUT to /service-config),
  # which is tens per day at fleet scale. Module defaults are fine.
  iot_sql = "SELECT *, topic(2) as device_id FROM 'devices/+/cmd-result'"

  tags = var.tags
}

# ── Health-probes pipeline (Phase 2, Issue #19) ────────────────────────────

module "sqs_health_probes" {
  source = "../terraform/modules/sqs-ingest"

  name          = "uknomi-cp-health-probes"
  iot_rule_name = "uknomi_cp_health_probes"
  # Agent publishes its healthprobes.Report payload on
  # devices/{device_id}/health-probes. The body already carries a
  # correlation_id and device_id, so no topic-derived columns are needed.
  # Cadence is 5 minutes (one report per device), same throughput class as
  # service-status; module defaults are fine.
  iot_sql = "SELECT * FROM 'devices/+/health-probes'"

  tags = var.tags
}

module "sqs_camera_status" {
  source = "../terraform/modules/sqs-ingest"

  name          = "uknomi-cp-camera-status"
  iot_rule_name = "uknomi_cp_camera_status"
  # Agent publishes its camerastatus.Report payload on
  # devices/{device_id}/camera-status (#113, PRD #111). The body already
  # carries a correlation_id and device_id, so no topic-derived columns are
  # needed. Cadence is minutes-scale (one report per device, far slower than
  # a live view); module defaults are fine.
  iot_sql = "SELECT * FROM 'devices/+/camera-status'"

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

  health_probes_queue_url = module.sqs_health_probes.queue_url
  health_probes_dlq_url   = module.sqs_health_probes.dlq_url

  cmd_result_queue_url = module.sqs_cmd_result.queue_url
  cmd_result_dlq_url   = module.sqs_cmd_result.dlq_url

  camera_status_queue_url = module.sqs_camera_status.queue_url
  camera_status_dlq_url   = module.sqs_camera_status.dlq_url

  # Postgres password from the RDS-managed master secret (issue #49); the
  # non-secret parts ride as plain env. No hand-synced db-dsn to go stale.
  db_password_secret_arn = aws_db_instance.main.master_user_secret[0].secret_arn
  db_host                = aws_db_instance.main.address
  db_port                = tostring(aws_db_instance.main.port)
  db_name                = aws_db_instance.main.db_name
  db_user                = aws_db_instance.main.username

  # Agent fleet-update reconcile (#40/#41): re-push signed agent.update to
  # drifted devices. Set together — a verifying agent rejects unsigned.
  agent_dist_bucket         = aws_s3_bucket.main["agent-dist"].bucket
  command_signing_secret_id = "uknomi/cp/command-signing-key"

  # Captures pipeline (#8): enables upload.request presign + upload.complete
  # indexing against the captures bucket.
  captures_bucket = aws_s3_bucket.main["captures"].bucket

  # Fleet notifications (#98/#99): the verified SES sender identity. Empty until
  # the operator verifies an identity + exits the SES sandbox (ADR-039); the
  # reconciler then sends email, and Teams already works off the webhook URL in
  # cp_settings.
  notifications_from_address = var.notifications_from_address

  desired_count = 1

  tags = var.tags
}
