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
      ]

      # The DSN carries the DB password — injected from Secrets Manager,
      # never baked into the task definition.
      secrets = [
        { name = "DB_DSN", valueFrom = var.db_dsn_secret_arn },
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
