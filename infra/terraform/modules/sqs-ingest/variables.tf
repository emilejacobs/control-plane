variable "name" {
  description = "Base name for the ingest queue. The DLQ is named \"<name>-dlq\"."
  type        = string
}

variable "iot_rule_name" {
  description = "Name of the IoT topic rule. AWS allows only letters, digits, and underscores here — no hyphens."
  type        = string
  validation {
    condition     = can(regex("^[a-zA-Z0-9_]+$", var.iot_rule_name))
    error_message = "iot_rule_name must contain only letters, digits, and underscores."
  }
}

variable "iot_sql" {
  description = "IoT SQL statement selecting the source messages, e.g. \"SELECT *, topic(2) as device_id FROM 'devices/+/telemetry'\"."
  type        = string
}

variable "max_receive_count" {
  description = "Deliveries attempted before SQS redrives a message to the DLQ."
  type        = number
  default     = 5
}

variable "visibility_timeout_seconds" {
  description = "How long a received message stays invisible before it can be redelivered."
  type        = number
  default     = 30
}

variable "message_retention_seconds" {
  description = "How long the main queue retains an unconsumed message (default 4 days)."
  type        = number
  default     = 345600
}

variable "tags" {
  description = "Tags applied to the queues and IAM role."
  type        = map(string)
  default     = {}
}
