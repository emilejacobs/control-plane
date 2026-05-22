variable "region" {
  description = "AWS region for IoT Core resources."
  type        = string
  default     = "us-east-1"
}

variable "device_id" {
  description = "Thing name / device_id (e.g. dev-mac-mini-emile, dev-pi-emile)."
  type        = string
  validation {
    condition     = length(var.device_id) > 0 && can(regex("^[a-zA-Z0-9_:-]+$", var.device_id))
    error_message = "device_id must be non-empty and match AWS IoT thing-name allowed characters."
  }
}

variable "bootstrap_ci_principal_arns" {
  description = "AWS principal ARNs the mac-mini-rollout CI authenticates as — allowed to assume the bootstrap-key read role (ADR-017)."
  type        = list(string)
}
