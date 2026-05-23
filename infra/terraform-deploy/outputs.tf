output "vpc_id" {
  description = "Control Plane VPC id — referenced by subsequent slices (RDS subnet group, ALB target groups, etc.)."
  value       = aws_vpc.main.id
}

output "public_subnet_ids" {
  description = "Public subnet ids, one per AZ. ALB lives here; NAT lives in [0]."
  value       = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  description = "Private subnet ids, one per AZ. Fargate tasks and RDS live here."
  value       = aws_subnet.private[*].id
}

output "alb_security_group_id" {
  description = "ALB security group id — consumed by the ALB slice."
  value       = aws_security_group.alb.id
}

output "tasks_security_group_id" {
  description = "Fargate tasks security group id — consumed by every CP task service."
  value       = aws_security_group.tasks.id
}

output "rds_security_group_id" {
  description = "RDS security group id — consumed by the Postgres slice."
  value       = aws_security_group.rds.id
}

output "tailscale_security_group_id" {
  description = "Tailscale subnet router security group id — consumed by that slice."
  value       = aws_security_group.tailscale.id
}

# ── KMS + Secrets (step 3) ──────────────────────────────────────────────────

output "kms_key_arn" {
  description = "Customer-managed KMS key ARN. Referenced by RDS (step 4), S3 (step 12), and the Secrets Manager secrets in this slice."
  value       = aws_kms_key.main.arn
}

output "kms_key_alias" {
  description = "KMS key alias — the indirection layer in case the key is rotated to a successor key."
  value       = aws_kms_alias.main.name
}

output "jwt_signing_key_secret_arn" {
  description = "Secrets Manager ARN of the JWT signing key. The cp-api task definition (step 8) injects it via the `secrets` attribute."
  value       = aws_secretsmanager_secret.jwt_signing_key.arn
}

output "totp_encryption_key_secret_arn" {
  description = "Secrets Manager ARN of the TOTP encryption key. The cp-api task definition (step 8) injects it."
  value       = aws_secretsmanager_secret.totp_encryption_key.arn
}

output "tailscale_auth_key_secret_arn" {
  description = "Secrets Manager ARN of the Tailscale auth key. The subnet-router task (step 11) reads it at startup."
  value       = aws_secretsmanager_secret.tailscale_auth_key.arn
}

# ── RDS (step 4) ────────────────────────────────────────────────────────────

output "rds_endpoint" {
  description = "Postgres endpoint host:port. Component of the constructed DB DSN."
  value       = aws_db_instance.main.endpoint
}

output "rds_master_user_secret_arn" {
  description = "Secrets Manager ARN of the RDS-managed master credential (JSON: {username, password})."
  value       = aws_db_instance.main.master_user_secret[0].secret_arn
}

output "db_dsn_secret_arn" {
  description = "Secrets Manager ARN of the constructed Postgres DSN. cp-api / cp-ingest read this; consumed by cp-ingest-service module."
  value       = aws_secretsmanager_secret.db_dsn.arn
}

# ── ECR (step 5) ────────────────────────────────────────────────────────────

output "ecr_repository_urls" {
  description = "Map of service name to ECR repository URL. CI (#02) pushes images here; task definitions reference the URL + tag."
  value       = { for k, r in aws_ecr_repository.main : k => r.repository_url }
}

# ── ECS (step 6) ────────────────────────────────────────────────────────────

output "ecs_cluster_arn" {
  description = "ECS Fargate cluster ARN. Consumed by every service slice."
  value       = aws_ecs_cluster.main.arn
}

output "task_execution_role_arn" {
  description = "Task execution role ARN. Every service uses this for image pulls, log writes, and secret fetches."
  value       = aws_iam_role.task_execution.arn
}

output "service_log_group_names" {
  description = "Map of service name to its CloudWatch log group. Task definitions reference these in the awslogs driver."
  value       = { for k, g in aws_cloudwatch_log_group.service : k => g.name }
}

# ── Task roles (step 7) ─────────────────────────────────────────────────────

output "cp_api_task_role_arn" { value = aws_iam_role.cp_api.arn }
output "cp_ingest_task_role_arn" { value = aws_iam_role.cp_ingest.arn }
output "dashboard_task_role_arn" { value = aws_iam_role.dashboard.arn }
output "tailscale_task_role_arn" { value = aws_iam_role.tailscale.arn }

# ── ALB / DNS / cp-api (step 8) ─────────────────────────────────────────────

output "alb_dns_name" {
  description = "The ALB's DNS name. Sanity-check by curl'ing it; production traffic uses the Route 53 alias records."
  value       = aws_lb.main.dns_name
}

output "control_zone_id" {
  description = "Route 53 hosted zone id for control.uknomi.com."
  value       = aws_route53_zone.control.zone_id
}

output "control_zone_nameservers" {
  description = "Add these four NS records at the uknomi.com registrar for `control` to delegate the sub-zone to AWS. Required before the ACM cert can validate."
  value       = aws_route53_zone.control.name_servers
}

output "control_acm_certificate_arn" {
  description = "ACM certificate ARN covering control.uknomi.com + api.control.uknomi.com."
  value       = aws_acm_certificate.control.arn
}

output "cp_api_target_group_arn" {
  description = "cp-api target group ARN."
  value       = aws_lb_target_group.cp_api.arn
}

# ── cp-ingest + IoT rules (step 10) ─────────────────────────────────────────

output "heartbeat_queue_url" { value = module.sqs_heartbeat.queue_url }
output "heartbeat_dlq_arn" { value = module.sqs_heartbeat.dlq_arn }
output "lifecycle_queue_url" { value = module.sqs_lifecycle.queue_url }
output "lifecycle_dlq_arn" { value = module.sqs_lifecycle.dlq_arn }
output "cp_ingest_service_name" { value = module.cp_ingest.service_name }
