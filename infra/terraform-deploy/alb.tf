# The Control Plane application load balancer. One ALB carries both the
# cp-api and dashboard surfaces; host-based listener rules in the service
# files route each hostname to its target group. Default action is a fixed
# 404 so an unmatched host gets a clear answer rather than a stack trace.

resource "aws_lb" "main" {
  name               = "uknomi-cp"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = aws_subnet.public[*].id

  enable_deletion_protection = true

  tags = { Name = "uknomi-cp" }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate_validation.control.certificate_arn

  default_action {
    type = "fixed-response"
    fixed_response {
      content_type = "text/plain"
      message_body = "Not found"
      status_code  = "404"
    }
  }

  tags = { Name = "uknomi-cp-https" }
}
