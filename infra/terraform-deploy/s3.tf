# S3 buckets for the CP. All three are versioned, encrypted (AES-256
# via S3-managed keys — switch to our KMS key in a future hardening
# pass once the KMS key policy adds the S3 service principal), and
# blocked from any form of public access.
#
# - audit-mirror     daily dump of the structured-log audit lines that
#                    cp-api emits. The audit_log table from #20 is the
#                    primary store; the S3 mirror is the cold copy.
# - command-output   Phase 3 readiness for capturing remote-command
#                    stdout/stderr. No writers in Phase 1.
# - agent-dist       Phase 3 readiness for signed agent self-update
#                    manifests. No writers in Phase 1.

locals {
  buckets = {
    audit-mirror   = "uknomi-cp-audit-mirror-${data.aws_caller_identity.current.account_id}"
    command-output = "uknomi-cp-command-output-${data.aws_caller_identity.current.account_id}"
    agent-dist     = "uknomi-cp-agent-dist-${data.aws_caller_identity.current.account_id}"
  }
}

resource "aws_s3_bucket" "main" {
  for_each = local.buckets
  bucket   = each.value
  # Object-lock must be enabled at bucket creation; enabling it on an
  # existing bucket is not supported by the S3 API. The audit-mirror
  # bucket carries it from day one for the Issue 28 retention policy;
  # the other two have no use case yet so we don't pay the lock-state
  # complexity.
  object_lock_enabled = each.key == "audit-mirror"
  tags = {
    Name    = each.value
    Purpose = each.key
  }
}

# 1-year governance-mode retention on the audit-mirror bucket (Issue 28).
# Every new object inherits the horizon automatically. Governance — not
# compliance — keeps an IAM "root override" escape hatch for the cases
# where an operator deliberately re-exports or deletes a day.
resource "aws_s3_bucket_object_lock_configuration" "audit_mirror" {
  bucket = aws_s3_bucket.main["audit-mirror"].id

  rule {
    default_retention {
      mode = "GOVERNANCE"
      days = 365
    }
  }
}

resource "aws_s3_bucket_versioning" "main" {
  for_each = local.buckets
  bucket   = aws_s3_bucket.main[each.key].id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "main" {
  for_each = local.buckets
  bucket   = aws_s3_bucket.main[each.key].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "main" {
  for_each                = local.buckets
  bucket                  = aws_s3_bucket.main[each.key].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
