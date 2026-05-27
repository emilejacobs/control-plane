# ECR repositories for the CP container images. One per service. Scanning
# enabled on push (free with the enhanced scanning feature off — just basic).
# Lifecycle keeps history bounded so storage cost stays flat.
#
# Default AES-256 encryption (AWS-managed) rather than our KMS key — images
# are not customer data and the integration cost (ECR principal in the key
# policy) is not worth the marginal hardening for Phase 1.

locals {
  ecr_repos = toset(["cp-api", "cp-ingest", "dashboard", "audit-mirror", "taxonomy-sync"])
}

resource "aws_ecr_repository" "main" {
  for_each = local.ecr_repos

  name                 = "uknomi/${each.key}"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = { Name = "uknomi-${each.key}" }
}

resource "aws_ecr_lifecycle_policy" "main" {
  for_each   = local.ecr_repos
  repository = aws_ecr_repository.main[each.key].name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images older than 7 days."
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 7
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep the 10 newest tagged images; expire the rest."
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}
