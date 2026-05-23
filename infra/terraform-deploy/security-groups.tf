# Security groups for the Control Plane deployment. Each tier is its own SG
# so future cross-tier rules (e.g. cp-api → Tailscale subnet router) can be
# added by referencing SG ids rather than re-CIDR-ing.

# ── ALB ─────────────────────────────────────────────────────────────────────

resource "aws_security_group" "alb" {
  name        = "uknomi-cp-alb"
  description = "Control Plane ALB -HTTPS from the public internet."
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "Egress to task SG on the listener ports (AWS does not let us scope egress to SG id only in inline blocks; ACL-style enforcement is on the receiving SG)."
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "uknomi-cp-alb" }
}

# ── Fargate tasks ───────────────────────────────────────────────────────────
# One SG shared by every CP task. Ingress is from the ALB only, on the two
# listener ports (cp-api on 8080, dashboard on 3000). Egress is open -the
# tasks need RDS, AWS APIs (via VPC endpoints), and the internet (SQS, IoT
# Core, etc.) via NAT.

resource "aws_security_group" "tasks" {
  name        = "uknomi-cp-tasks"
  description = "Control Plane Fargate tasks -ingress only from the ALB SG."
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "cp-api app port from the ALB"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  ingress {
    description     = "Dashboard app port from the ALB"
    from_port       = 3000
    to_port         = 3000
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  egress {
    description = "All egress -RDS via the rds SG; AWS APIs via VPC endpoints; the rest via NAT."
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "uknomi-cp-tasks" }
}

# ── RDS Postgres ────────────────────────────────────────────────────────────
# Ingress on 5432 from the task SG only. No egress -the DB does not
# initiate traffic. The RDS instance itself lands in the next slice.

resource "aws_security_group" "rds" {
  name        = "uknomi-cp-rds"
  description = "Control Plane Postgres -5432 from the task SG only."
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "Postgres from tasks"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.tasks.id]
  }

  tags = { Name = "uknomi-cp-rds" }
}

# ── Tailscale subnet router ─────────────────────────────────────────────────
# The subnet router only initiates traffic -to the Tailscale coordination
# servers and to tailnet peers. No public ingress; tasks reach the tailnet
# through the VPC route table, not through this SG.

resource "aws_security_group" "tailscale" {
  name        = "uknomi-cp-tailscale"
  description = "Control Plane Tailscale subnet router -egress only."
  vpc_id      = aws_vpc.main.id

  egress {
    description = "All egress (Tailscale coordination + tailnet peers)."
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "uknomi-cp-tailscale" }
}

# ── VPC interface endpoints ─────────────────────────────────────────────────
# Each interface endpoint exposes its AWS API on a private IP per AZ; this
# SG controls who can reach those IPs. HTTPS from the task SG only.

resource "aws_security_group" "vpc_endpoints" {
  name        = "uknomi-cp-vpc-endpoints"
  description = "Ingress to interface VPC endpoints from the task SG."
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "HTTPS from tasks"
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    security_groups = [aws_security_group.tasks.id]
  }

  tags = { Name = "uknomi-cp-vpc-endpoints" }
}
