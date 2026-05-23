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
