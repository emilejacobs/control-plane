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
