# Per-service task roles — the IAM identity each container assumes for
# application-level AWS API calls. (The task *execution* role in ecs.tf is
# separate; it is what ECS itself assumes to start the container.)

data "aws_iam_policy_document" "task_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# ── cp-api ──────────────────────────────────────────────────────────────────
# IoT for enrolment minting (create thing + cert, attach policy, revoke).
# Secrets Manager + KMS for the bootstrap-key refresh-on-mismatch path that
# bootstrap.SecretsManagerLoader uses from inside the cp-api process.

resource "aws_iam_role" "cp_api" {
  name               = "uknomi-cp-api"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
  tags               = { Name = "uknomi-cp-api" }
}

data "aws_iam_policy_document" "cp_api" {
  statement {
    sid = "IoTEnrolmentMint"
    actions = [
      "iot:CreateThing",
      "iot:DescribeThing",
      "iot:CreateKeysAndCertificate",
      "iot:AttachThingPrincipal",
      "iot:UpdateCertificate",
      "iot:DescribeEndpoint",
    ]
    resources = ["*"]
  }
  statement {
    sid     = "IoTAttachAgentPolicyOnly"
    actions = ["iot:AttachPolicy", "iot:DetachPolicy"]
    # AWS evaluates iot:AttachPolicy against BOTH the policy and the target
    # (cert) resource — listing only the policy ARN here denies on the cert
    # side. The cert wildcard is bounded because the only action allowed on
    # it is AttachPolicy/DetachPolicy of UknomiAgentPolicy, not full mgmt.
    resources = [
      "arn:aws:iot:${var.region}:${data.aws_caller_identity.current.account_id}:policy/UknomiAgentPolicy",
      "arn:aws:iot:${var.region}:${data.aws_caller_identity.current.account_id}:cert/*",
    ]
  }
  statement {
    sid       = "BootstrapKeyRefresh"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = ["arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:uknomi/cp/bootstrap-key*"]
  }
  statement {
    sid       = "DecryptForBootstrapRefresh"
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.main.arn]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["secretsmanager.${var.region}.amazonaws.com"]
    }
  }
  # Phase 2 slice 2: PUT /devices/{id}/service-config publishes a
  # config.update envelope on devices/{id}/cmd via iotdataplane. Scope
  # restricted to the cmd topic for the devices/* prefix — cp-api has
  # no business publishing on telemetry / service-status / cmd-result
  # paths (those flow agent → cp).
  statement {
    sid     = "IoTPublishCmd"
    actions = ["iot:Publish"]
    resources = [
      "arn:aws:iot:${var.region}:${data.aws_caller_identity.current.account_id}:topic/devices/*/cmd",
    ]
  }
  # ADR-033 § 3 — "Force sync now" button. cp-api runs the same
  # cp-taxonomy-sync task def the EventBridge schedule fires; the
  # task itself handles concurrency via pg_try_advisory_lock.
  statement {
    sid       = "TaxonomyRunTask"
    actions   = ["ecs:RunTask"]
    resources = ["${aws_ecs_task_definition.taxonomy_sync.arn_without_revision}:*"]
    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [aws_ecs_cluster.main.arn]
    }
  }
  statement {
    sid       = "TaxonomyPassRole"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.task_execution.arn, aws_iam_role.taxonomy_sync.arn]
  }
  # Agent fleet-update (#40/#41): the rollout Pusher reads the signed release
  # manifest + presigns the binary from agent-dist, and reads the
  # command-signing key to sign the agent.update envelope. iot:Publish (above)
  # already covers pushing the command; kms:Decrypt (above, ViaService
  # secretsmanager) already covers decrypting the command-signing secret.
  statement {
    sid       = "AgentDistRead"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.main["agent-dist"].arn}/agent/*"]
  }
  # GET /fleet/agent-versions (#42) lists the published versions in the catalog
  # via ListObjectsV2 over the agent/ prefix — which needs s3:ListBucket on the
  # bucket itself (GetObject above only covers reading a known key). Scoped with
  # a prefix condition so the role can only enumerate under agent/.
  statement {
    sid       = "AgentDistList"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.main["agent-dist"].arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["agent/", "agent/*"]
    }
  }
  statement {
    sid       = "CommandSigningKeyRead"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = ["arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:uknomi/cp/command-signing-key*"]
  }
  # Captures surface (#8): cp-api presigns S3 URLs whose signature inherits this
  # role's permissions. GET /captures/{id}/url signs a download (s3:GetObject);
  # POST /devices/{id}/snapshot signs the agent's upload PUT (s3:PutObject) for
  # the on-demand camera snapshot. Both are needed or the presigned URL 403s when
  # the agent/browser uses it.
  statement {
    sid       = "CapturesReadWrite"
    actions   = ["s3:GetObject", "s3:PutObject"]
    resources = ["${aws_s3_bucket.main["captures"].arn}/*"]
  }
}

resource "aws_iam_role_policy" "cp_api" {
  name   = "cp-api-runtime"
  role   = aws_iam_role.cp_api.id
  policy = data.aws_iam_policy_document.cp_api.json
}

# ── cp-ingest ───────────────────────────────────────────────────────────────
# SQS receive + delete on the two queues. Scoped by name prefix
# (uknomi-cp-*) so the role works whether the queues are wired here or via
# the modules in step 10 — and stays narrow either way.

resource "aws_iam_role" "cp_ingest" {
  name               = "uknomi-cp-ingest"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
  tags               = { Name = "uknomi-cp-ingest" }
}

data "aws_iam_policy_document" "cp_ingest" {
  statement {
    sid = "SqsConsume"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
      "sqs:ChangeMessageVisibility",
      "sqs:SendMessage", # for DLQ writes from the consumer
    ]
    resources = ["arn:aws:sqs:${var.region}:${data.aws_caller_identity.current.account_id}:uknomi-cp-*"]
  }
  # Agent fleet-update reconcile (#40/#41): on reconnect/heartbeat drift,
  # cp-ingest re-pushes a signed agent.update. It reads the release manifest +
  # presigns from agent-dist, reads + decrypts the command-signing key, and
  # publishes the signed command on the device cmd topic.
  statement {
    sid       = "AgentDistRead"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.main["agent-dist"].arn}/agent/*"]
  }
  statement {
    sid       = "CommandSigningKeyRead"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = ["arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:uknomi/cp/command-signing-key*"]
  }
  statement {
    sid       = "DecryptCommandSigningKey"
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.main.arn]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["secretsmanager.${var.region}.amazonaws.com"]
    }
  }
  statement {
    sid       = "IoTPublishCmd"
    actions   = ["iot:Publish"]
    resources = ["arn:aws:iot:${var.region}:${data.aws_caller_identity.current.account_id}:topic/devices/*/cmd"]
  }
  # Captures pipeline (#8): cp-ingest presigns the agent's upload PUT, so its
  # role must itself hold s3:PutObject on the captures bucket for the signed URL
  # to resolve. (Publishing the resulting upload.url reuses IoTPublishCmd above.)
  statement {
    sid       = "CapturesWrite"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.main["captures"].arn}/*"]
  }
}

resource "aws_iam_role_policy" "cp_ingest" {
  name   = "cp-ingest-runtime"
  role   = aws_iam_role.cp_ingest.id
  policy = data.aws_iam_policy_document.cp_ingest.json
}

# ── dashboard ───────────────────────────────────────────────────────────────
# The Next.js dashboard makes no direct AWS calls — it talks to cp-api only.
# An empty task role still must exist for ECS.

resource "aws_iam_role" "dashboard" {
  name               = "uknomi-cp-dashboard"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
  tags               = { Name = "uknomi-cp-dashboard" }
}

# ── tailscale-subnet-router ─────────────────────────────────────────────────
# The Tailscale binary itself needs no AWS API access — the auth key is
# injected as an env var via the execution role's Secrets Manager path.

resource "aws_iam_role" "tailscale" {
  name               = "uknomi-cp-tailscale"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
  tags               = { Name = "uknomi-cp-tailscale" }
}
