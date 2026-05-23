terraform {
  required_version = ">= 1.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }

  # Remote state on S3, with DynamoDB-based locking. The bucket and table
  # are created once, out-of-band — see README § "State backend bootstrap".
  # `key` is per-root so future Terraform roots (e.g. the Phase 1 deployment
  # infra) live next to this one in the same bucket without collision.
  backend "s3" {
    bucket         = "uknomi-tfstate-523612763411"
    key            = "iot-core/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "uknomi-tfstate-locks"
    encrypt        = true
  }
}

provider "aws" {
  region = var.region
}
