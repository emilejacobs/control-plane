output "queue_url" {
  description = "URL of the main ingest queue — pass to the consumer as HEARTBEAT_QUEUE_URL."
  value       = aws_sqs_queue.main.id
}

output "queue_arn" {
  description = "ARN of the main ingest queue."
  value       = aws_sqs_queue.main.arn
}

output "dlq_url" {
  description = "URL of the dead-letter queue — pass to the consumer as HEARTBEAT_DLQ_URL."
  value       = aws_sqs_queue.dlq.id
}

output "dlq_arn" {
  description = "ARN of the dead-letter queue (alarm on its depth — PRD § Hardening)."
  value       = aws_sqs_queue.dlq.arn
}
