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
