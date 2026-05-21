terraform {
  required_version = ">= 1.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }

  # Local state for the Phase 0 spike. Move to S3 + DynamoDB before a second
  # person touches this — see Phase 1 issue 01.
  # backend "s3" { ... }
}

provider "aws" {
  region = var.region
}
