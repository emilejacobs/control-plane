variable "region" {
  description = "AWS region for the deployment."
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  description = "CIDR block for the Control Plane VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "az_count" {
  description = "Number of AZs the VPC spans. Two is the minimum for ALB + multi-AZ RDS; raise to three before a fleet rollout that demands higher availability."
  type        = number
  default     = 2
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for the public subnets, one per AZ. Length must be >= az_count."
  type        = list(string)
  default     = ["10.0.0.0/24", "10.0.1.0/24"]
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for the private subnets, one per AZ. Length must be >= az_count."
  type        = list(string)
  default     = ["10.0.10.0/24", "10.0.11.0/24"]
}

variable "tags" {
  description = "Common tags applied to every resource in this root."
  type        = map(string)
  default = {
    Project = "uknomi"
    Service = "control-plane"
    Phase   = "1"
    Managed = "terraform"
  }
}

# ── RDS (step 4) ────────────────────────────────────────────────────────────

variable "db_instance_class" {
  description = "RDS Postgres instance class. db.t4g.micro is comfortable for Phase 1 (25 Macs, low write rate); resize before Wave 2 if needed."
  type        = string
  default     = "db.t4g.micro"
}

variable "db_engine_version" {
  description = "Major Postgres version. AWS picks the latest matching minor."
  type        = string
  default     = "16"
}

variable "db_allocated_storage" {
  description = "Initial GB of GP3 storage."
  type        = number
  default     = 20
}

variable "db_max_allocated_storage" {
  description = "Storage autoscaling cap in GB."
  type        = number
  default     = 100
}

variable "db_multi_az" {
  description = "Multi-AZ deployment. Single-AZ for Wave 0; flip to true before the ship gate."
  type        = bool
  default     = false
}

variable "db_backup_retention_days" {
  description = "Daily automated-backup retention."
  type        = number
  default     = 7
}

variable "db_log_retention_days" {
  description = "CloudWatch retention for the RDS postgresql log export."
  type        = number
  default     = 30
}
