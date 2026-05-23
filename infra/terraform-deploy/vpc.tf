# The Control Plane VPC: a private network for Fargate + RDS, with a public
# tier for the ALB and a single NAT gateway for egress from the private tier.
# Two AZs are the floor for ALB + multi-AZ RDS; the az_count variable allows
# expansion without a structural change.

data "aws_availability_zones" "available" {
  state = "available"
}

# ── VPC + edge gateways ─────────────────────────────────────────────────────

resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = { Name = "uknomi-cp" }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags   = { Name = "uknomi-cp" }
}

# ── Subnets ─────────────────────────────────────────────────────────────────

resource "aws_subnet" "public" {
  count                   = var.az_count
  vpc_id                  = aws_vpc.main.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = false

  tags = {
    Name = "uknomi-cp-public-${data.aws_availability_zones.available.names[count.index]}"
    Tier = "public"
  }
}

resource "aws_subnet" "private" {
  count             = var.az_count
  vpc_id            = aws_vpc.main.id
  cidr_block        = var.private_subnet_cidrs[count.index]
  availability_zone = data.aws_availability_zones.available.names[count.index]

  tags = {
    Name = "uknomi-cp-private-${data.aws_availability_zones.available.names[count.index]}"
    Tier = "private"
  }
}

# ── NAT ─────────────────────────────────────────────────────────────────────
# Single NAT in AZ-0 by design. Phase 1's cost posture says one is enough;
# the single-AZ chokepoint is acceptable because a NAT-AZ outage degrades
# but does not break the CP (the agents reconnect through IoT Core; the CP
# only loses egress for AWS APIs not covered by the VPC endpoints below).
# Promote to per-AZ NAT before traffic or HA requirements demand it.

resource "aws_eip" "nat" {
  domain = "vpc"
  tags   = { Name = "uknomi-cp-nat" }
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id
  tags          = { Name = "uknomi-cp" }

  # An IGW must exist before the NAT can route to the internet.
  depends_on = [aws_internet_gateway.main]
}

# ── Route tables ────────────────────────────────────────────────────────────

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = { Name = "uknomi-cp-public" }
}

resource "aws_route_table_association" "public" {
  count          = var.az_count
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main.id
  }

  tags = { Name = "uknomi-cp-private" }
}

resource "aws_route_table_association" "private" {
  count          = var.az_count
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

# ── VPC endpoints ───────────────────────────────────────────────────────────
# Gateway endpoints are free (S3, DynamoDB) — always worth wiring up.
# Interface endpoints cost ~$7/mo/AZ; included for the AWS APIs the CP
# services hit frequently (Secrets Manager at startup + refresh, ECR for
# image pulls, CloudWatch Logs continuously). SQS is intentionally
# excluded — cp-ingest's poll traffic runs through NAT to keep the
# endpoint bill bounded.

resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.main.id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = [aws_route_table.private.id]
  tags              = { Name = "uknomi-cp-s3" }
}

resource "aws_vpc_endpoint" "dynamodb" {
  vpc_id            = aws_vpc.main.id
  service_name      = "com.amazonaws.${var.region}.dynamodb"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = [aws_route_table.private.id]
  tags              = { Name = "uknomi-cp-dynamodb" }
}

locals {
  interface_endpoints = toset([
    "secretsmanager",
    "ecr.api",
    "ecr.dkr",
    "logs",
  ])
}

resource "aws_vpc_endpoint" "interface" {
  for_each            = local.interface_endpoints
  vpc_id              = aws_vpc.main.id
  service_name        = "com.amazonaws.${var.region}.${each.key}"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = aws_subnet.private[*].id
  security_group_ids  = [aws_security_group.vpc_endpoints.id]
  private_dns_enabled = true
  tags                = { Name = "uknomi-cp-${each.key}" }
}
