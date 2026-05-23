terraform {
  required_version = ">= 1.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }

  # Same state bucket as the IoT Core root; distinct key so the two roots
  # do not collide. See `infra/terraform/README.md` § "State backend
  # bootstrap" for the one-time bucket + lock-table setup.
  backend "s3" {
    bucket         = "uknomi-tfstate-523612763411"
    key            = "deploy/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "uknomi-tfstate-locks"
    encrypt        = true
  }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = var.tags
  }
}
