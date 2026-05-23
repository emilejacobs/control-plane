# Dashboard service — same shape as cp-api but on the apex
# control.uknomi.com hostname. No secrets needed; the dashboard is a thin
# Next.js client that talks to cp-api at https://api.control.uknomi.com.
#
# Caveat for the real image: NEXT_PUBLIC_API_URL is baked in at Next.js
# build time, not read at runtime — so the CI in #02 must build the
# dashboard image with NEXT_PUBLIC_API_URL=https://api.control.uknomi.com.
# Setting it as a Fargate env var (below) is a no-op for the bundled
# client; it is kept here as documentation of the expected value.

resource "aws_lb_target_group" "dashboard" {
  name        = "uknomi-cp-dashboard"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = aws_vpc.main.id

  health_check {
    path                = "/"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
    matcher             = "200-399"
  }

  tags = { Name = "uknomi-cp-dashboard" }
}

resource "aws_lb_listener_rule" "dashboard" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 101

  condition {
    host_header {
      values = ["control.uknomi.com"]
    }
  }

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.dashboard.arn
  }
}

resource "aws_ecs_task_definition" "dashboard" {
  family                   = "uknomi-cp-dashboard"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.dashboard.arn

  container_definitions = jsonencode([
    {
      name      = "dashboard"
      image     = "public.ecr.aws/nginx/nginx-unprivileged:latest" # placeholder; CI (#02) replaces with ECR
      essential = true

      portMappings = [{
        containerPort = 8080 # placeholder; real dashboard image will listen on 3000 — update here when CI lands
        protocol      = "tcp"
      }]

      environment = [
        { name = "NEXT_PUBLIC_API_URL", value = "https://api.control.uknomi.com" },
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.service["dashboard"].name
          awslogs-region        = var.region
          awslogs-stream-prefix = "dashboard"
        }
      }
    }
  ])

  tags = { Name = "uknomi-cp-dashboard" }
}

resource "aws_ecs_service" "dashboard" {
  name            = "dashboard"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.dashboard.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.dashboard.arn
    container_name   = "dashboard"
    container_port   = 8080
  }

  depends_on = [aws_lb_listener_rule.dashboard]

  tags = { Name = "uknomi-cp-dashboard" }
}
