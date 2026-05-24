# Tailscale subnet router — a Fargate task that joins the existing
# uKnomi tailnet and advertises the VPC's private CIDR as a route, so
# cp-api can reach device-local Edge UIs on the tailnet (architecture.md
# § Tailscale subnet router).
#
# Caveat: Fargate does not allow NET_ADMIN or /dev/net/tun, so the
# Tailscale binary runs in userspace mode (TS_USERSPACE=true). Userspace
# subnet routing has known limits compared with kernel-mode routing
# (proxied connections, not transparent forwarding) — fine for the
# in-process HTTPS calls cp-api makes to the Edge UI. If the proxy
# behavior turns out to matter for a future use case, this slice gets
# revisited to an ECS-on-EC2 launch type.
#
# State is ephemeral; on task restart Tailscale re-registers with the
# reusable auth key and gets a fresh node identity. Stale node entries
# get cleaned manually in the Tailscale admin console.

resource "aws_ecs_task_definition" "tailscale" {
  family                   = "uknomi-cp-tailscale"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.tailscale.arn

  container_definitions = jsonencode([
    {
      name      = "tailscale"
      image     = "tailscale/tailscale:stable"
      essential = true

      environment = [
        { name = "TS_USERSPACE", value = "true" },
        { name = "TS_ROUTES", value = var.vpc_cidr },
        { name = "TS_HOSTNAME", value = "uknomi-cp-subnet-router" },
        { name = "TS_ACCEPT_DNS", value = "false" },
        { name = "TS_STATE_DIR", value = "/var/lib/tailscale" },
        { name = "TS_EXTRA_ARGS", value = "--advertise-tags=tag:uknomi-cp" },
      ]

      secrets = [
        { name = "TS_AUTHKEY", valueFrom = aws_secretsmanager_secret.tailscale_auth_key.arn },
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.service["tailscale-subnet-router"].name
          awslogs-region        = var.region
          awslogs-stream-prefix = "tailscale"
        }
      }
    }
  ])

  tags = { Name = "uknomi-cp-tailscale" }
}

resource "aws_ecs_service" "tailscale" {
  name            = "tailscale-subnet-router"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.tailscale.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.tailscale.id]
    assign_public_ip = false
  }

  tags = { Name = "uknomi-cp-tailscale" }
}
