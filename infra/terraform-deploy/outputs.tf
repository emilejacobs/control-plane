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
