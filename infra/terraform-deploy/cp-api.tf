# cp-api service — target group, listener rule, task definition, ECS
# service. Image is pulled from ECR (#26's build/push workflow) at the
# tag named by var.image_tag; the ALB target group health-checks the
# /healthz handler (#26) with a strict 200 matcher.

resource "aws_lb_target_group" "cp_api" {
  name        = "uknomi-cp-api"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = aws_vpc.main.id

  health_check {
    path                = "/healthz"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
    matcher             = "200"
  }

  tags = { Name = "uknomi-cp-api" }
}

resource "aws_lb_listener_rule" "cp_api" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 100

  condition {
    host_header {
      values = ["api.control.uknomi.com"]
    }
  }

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.cp_api.arn
  }
}

resource "aws_ecs_task_definition" "cp_api" {
  family                   = "uknomi-cp-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "512"
  memory                   = "1024"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.cp_api.arn

  container_definitions = jsonencode([
    {
      name      = "cp-api"
      image     = "${aws_ecr_repository.main["cp-api"].repository_url}:${var.image_tag}"
      essential = true

      portMappings = [{
        containerPort = 8080
        protocol      = "tcp"
      }]

      environment = [
        { name = "PORT", value = "8080" },
        { name = "IOT_POLICY_NAME", value = "UknomiAgentPolicy" },
        { name = "AWS_REGION", value = var.region },
        { name = "CP_BOOTSTRAP_SECRET_ID", value = "uknomi/cp/bootstrap-key" },
        { name = "CORS_ALLOWED_ORIGINS", value = "https://control.uknomi.com" },
        # ADR-033 § 3 — taxonomy "Force sync now" button. Setting these
        # enables POST /taxonomy/sync; unsetting them disables the
        # route at cp-api startup (read surface keeps working).
        { name = "TAXONOMY_ECS_CLUSTER", value = aws_ecs_cluster.main.arn },
        { name = "TAXONOMY_ECS_TASK_DEF", value = aws_ecs_task_definition.taxonomy_sync.arn_without_revision },
        { name = "TAXONOMY_ECS_SUBNETS", value = join(",", aws_subnet.private[*].id) },
        { name = "TAXONOMY_ECS_SGS", value = aws_security_group.tasks.id },
      ]

      secrets = [
        { name = "DB_DSN", valueFrom = aws_secretsmanager_secret.db_dsn.arn },
        { name = "JWT_SIGNING_KEY", valueFrom = aws_secretsmanager_secret.jwt_signing_key.arn },
        { name = "TOTP_ENCRYPTION_KEY", valueFrom = aws_secretsmanager_secret.totp_encryption_key.arn },
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.service["cp-api"].name
          awslogs-region        = var.region
          awslogs-stream-prefix = "cp-api"
        }
      }
    }
  ])

  tags = { Name = "uknomi-cp-api" }
}

resource "aws_ecs_service" "cp_api" {
  name            = "cp-api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.cp_api.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.cp_api.arn
    container_name   = "cp-api"
    container_port   = 8080
  }

  # ECS service creation fails if the listener rule's target group is not
  # already attached to a listener — the rule must exist first.
  depends_on = [aws_lb_listener_rule.cp_api]

  tags = { Name = "uknomi-cp-api" }
}
