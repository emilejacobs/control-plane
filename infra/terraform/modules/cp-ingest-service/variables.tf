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

variable "lifecycle_queue_url" {
  description = "SQS URL of the presence-lifecycle queue (sqs-ingest module output queue_url)."
  type        = string
}

variable "lifecycle_dlq_url" {
  description = "SQS URL of the presence-lifecycle dead-letter queue (sqs-ingest module output dlq_url)."
  type        = string
}

# Postgres connection (issue #49). The password is injected from the
# RDS-managed master secret via a task-def secret reference; the rest is
# non-secret env. cp-ingest builds the DSN in-process (storage.ResolveDSN).
variable "db_password_secret_arn" {
  description = "ARN of the RDS-managed master secret; the module appends ':password::' to read the password JSON key."
  type        = string
}

variable "db_host" {
  description = "Postgres host (RDS endpoint address)."
  type        = string
}

variable "db_port" {
  description = "Postgres port."
  type        = string
  default     = "5432"
}

variable "db_name" {
  description = "Postgres database name."
  type        = string
  default     = "uknomi_cp"
}

variable "db_user" {
  description = "Postgres user."
  type        = string
  default     = "uknomi_admin"
}

variable "db_sslmode" {
  description = "libpq sslmode."
  type        = string
  default     = "require"
}

# Agent fleet-update reconcile (#40/#41). Empty defaults keep the module
# applyable before the feature is enabled — cp-ingest skips the re-push when
# AGENT_DIST_BUCKET is unset and publishes unsigned when the signing id is unset.
variable "agent_dist_bucket" {
  description = "S3 bucket holding the signed agent release catalog. Empty disables reconcile re-push."
  type        = string
  default     = ""
}

variable "command_signing_secret_id" {
  description = "Secrets Manager id of the Ed25519 command-signing key. Empty publishes unsigned."
  type        = string
  default     = ""
}

# Captures pipeline (#8). Empty default keeps the module applyable before the
# feature is enabled — cp-ingest ignores upload.request/complete when unset.
variable "captures_bucket" {
  description = "S3 bucket for the captures pipeline. Empty disables upload.request/complete handling."
  type        = string
  default     = ""
}

# Fleet notifications (#98/#99). Empty default keeps the module applyable before
# the SES sender identity is verified — cp-ingest skips the email channel when
# unset (Teams still delivers off the webhook URL in cp_settings).
variable "notifications_from_address" {
  description = "Verified SES sender identity for fleet-notification emails. Empty disables the email channel."
  type        = string
  default     = ""
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

# Phase 2 service-status reporting (Issue 01). Empty defaults so the
# module stays applyable in environments that haven't provisioned the
# service-status SQS queue yet — cp-ingest's main.go skips the consumer
# when these are unset.
variable "service_status_queue_url" {
  description = "SQS URL of the service-status queue (sqs-ingest module output queue_url). Optional."
  type        = string
  default     = ""
}

variable "service_status_dlq_url" {
  description = "SQS URL of the service-status dead-letter queue (sqs-ingest module output dlq_url). Optional."
  type        = string
  default     = ""
}

# Phase 2 fleet health probes (Issue #19). Empty defaults so the module
# stays applyable before the health-probes SQS queue is provisioned —
# cp-ingest's main.go skips the consumer when these are unset.
variable "health_probes_queue_url" {
  description = "SQS URL of the health-probes queue (sqs-ingest module output queue_url). Optional."
  type        = string
  default     = ""
}

variable "health_probes_dlq_url" {
  description = "SQS URL of the health-probes dead-letter queue (sqs-ingest module output dlq_url). Optional."
  type        = string
  default     = ""
}

# Phase 2 slice 2 cmd-result feedback (per-device allow-list overrides).
# Empty defaults keep cp-ingest's cmd-result consumer disabled until the
# deploy root wires the new sqs-ingest module + IoT Rule.
variable "cmd_result_queue_url" {
  description = "SQS URL of the cmd-result queue (sqs-ingest module output queue_url). Optional."
  type        = string
  default     = ""
}

variable "cmd_result_dlq_url" {
  description = "SQS URL of the cmd-result dead-letter queue (sqs-ingest module output dlq_url). Optional."
  type        = string
  default     = ""
}

# Camera observability (#113). Empty defaults keep cp-ingest's
# camera-status consumer disabled until the deploy root wires the new
# sqs-ingest module + IoT Rule.
variable "camera_status_queue_url" {
  description = "SQS URL of the camera-status queue (sqs-ingest module output queue_url). Optional."
  type        = string
  default     = ""
}

variable "camera_status_dlq_url" {
  description = "SQS URL of the camera-status dead-letter queue (sqs-ingest module output dlq_url). Optional."
  type        = string
  default     = ""
}
