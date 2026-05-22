output "service_name" {
  description = "Name of the cp-ingest ECS service."
  value       = aws_ecs_service.this.name
}

output "task_definition_arn" {
  description = "ARN of the cp-ingest task definition."
  value       = aws_ecs_task_definition.this.arn
}

output "log_group_name" {
  description = "CloudWatch log group carrying cp-ingest's structured JSON logs."
  value       = aws_cloudwatch_log_group.this.name
}
