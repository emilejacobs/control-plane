variable "image" {
  description = "Container image URI for cp-ingest."
  type        = string
}

variable "region" {
  description = "AWS region (used by the awslogs log driver)."
  type        = string
}

variable "cluster_arn" {
  description = "ECS cluster ARN to run the service in (provided by issue 01)."
  type        = string
}

variable "subnet_ids" {
  description = "Private subnet IDs for the Fargate task ENIs."
  type        = list(string)
}

variable "security_group_ids" {
  description = "Security group IDs for the Fargate task ENIs."
  type        = list(string)
}

variable "execution_role_arn" {
  description = "ECS task execution role ARN (image pull + log writes)."
  type        = string
}

variable "task_role_arn" {
  description = "Task role ARN granting cp-ingest SQS receive/delete/send and RDS access."
  type        = string
}

variable "heartbeat_queue_url" {
  description = "SQS URL of the presence-heartbeat queue (sqs-ingest module output queue_url)."
  type        = string
}

variable "heartbeat_dlq_url" {
  description = "SQS URL of the presence-heartbeat dead-letter queue (sqs-ingest module output dlq_url)."
  type        = string
}

variable "db_dsn_secret_arn" {
  description = "Secrets Manager ARN holding the Postgres DSN."
  type        = string
}

variable "desired_count" {
  description = "Number of cp-ingest tasks to run."
  type        = number
  default     = 1
}

variable "cpu" {
  description = "Fargate task CPU units."
  type        = number
  default     = 256
}

variable "memory" {
  description = "Fargate task memory in MiB."
  type        = number
  default     = 512
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention for cp-ingest."
  type        = number
  default     = 30
}

variable "tags" {
  description = "Tags applied to the service, task definition, and log group."
  type        = map(string)
  default     = {}
}
