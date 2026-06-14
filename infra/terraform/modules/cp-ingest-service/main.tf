# Module cp-ingest-service: the Fargate service running cmd/cp-ingest, the
# presence heartbeat consumer (ADR-018). The ECS cluster, VPC networking,
# and IAM roles are inputs — they are provisioned by the Phase 1 root in
# issue 01; this module only adds the cp-ingest workload onto them.
terraform {
  required_version = ">= 1.7"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

# Structured JSON logs from cplog land here (AC6: logs to CloudWatch Logs).
resource "aws_cloudwatch_log_group" "this" {
  name              = "/uknomi/cp-ingest"
  retention_in_days = var.log_retention_days
  tags              = var.tags
}

resource "aws_ecs_task_definition" "this" {
  family                   = "cp-ingest"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.task_role_arn
  tags                     = var.tags

  container_definitions = jsonencode([
    {
      name      = "cp-ingest"
      image     = var.image
      essential = true

      environment = [
        { name = "HEARTBEAT_QUEUE_URL", value = var.heartbeat_queue_url },
        { name = "HEARTBEAT_DLQ_URL", value = var.heartbeat_dlq_url },
        { name = "LIFECYCLE_QUEUE_URL", value = var.lifecycle_queue_url },
        { name = "LIFECYCLE_DLQ_URL", value = var.lifecycle_dlq_url },
        # Phase 2: empty strings keep cp-ingest's service-status consumer
        # silently disabled. The deploy root populates them once the new
        # sqs-ingest instantiation lands.
        { name = "SERVICE_STATUS_QUEUE_URL", value = var.service_status_queue_url },
        { name = "SERVICE_STATUS_DLQ_URL", value = var.service_status_dlq_url },
        # Phase 2 issue #19: health-probes. Empty disables the consumer;
        # populating both turns it on.
        { name = "HEALTH_PROBES_QUEUE_URL", value = var.health_probes_queue_url },
        { name = "HEALTH_PROBES_DLQ_URL", value = var.health_probes_dlq_url },
        # Phase 2 slice 2: cmd-result feedback. Same posture — empty
        # disables the consumer; populating both turns it on.
        { name = "CMD_RESULT_QUEUE_URL", value = var.cmd_result_queue_url },
        { name = "CMD_RESULT_DLQ_URL", value = var.cmd_result_dlq_url },
        # Postgres connection (issue #49). Non-secret parts; the password is
        # injected separately from the RDS-managed secret below, and
        # cp-ingest builds the DSN in-process via storage.ResolveDSN.
        { name = "DB_HOST", value = var.db_host },
        { name = "DB_PORT", value = var.db_port },
        { name = "DB_NAME", value = var.db_name },
        { name = "DB_USER", value = var.db_user },
        { name = "DB_SSLMODE", value = var.db_sslmode },
      ]

      # The DB password is injected from the RDS-managed master secret's
      # `password` JSON key — no hand-synced DSN copy to go stale on rotation.
      secrets = [
        { name = "DB_PASSWORD", valueFrom = "${var.db_password_secret_arn}:password::" },
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.this.name
          "awslogs-region"        = var.region
          "awslogs-stream-prefix" = "cp-ingest"
        }
      }
    }
  ])
}

resource "aws_ecs_service" "this" {
  name            = "cp-ingest"
  cluster         = var.cluster_arn
  task_definition = aws_ecs_task_definition.this.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = var.security_group_ids
    assign_public_ip = false
  }

  tags = var.tags
}
