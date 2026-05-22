# Module sqs-ingest: one ingest topic — a main SQS queue with a dead-letter
# queue, plus the IoT topic rule that feeds it. Reused per ingest topic
# (PRD § Infra: "reusable SQS + DLQ + IoT-Rule wiring per ingest topic").
terraform {
  required_version = ">= 1.7"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

# Dead-letter queue: holds poison messages (the cp-ingest consumer routes
# them here explicitly, and SQS redrive lands repeatedly-failing messages
# here too). Retained the full 14 days so they can be inspected.
resource "aws_sqs_queue" "dlq" {
  name                      = "${var.name}-dlq"
  message_retention_seconds = 1209600
  tags                      = var.tags
}

resource "aws_sqs_queue" "main" {
  name                       = var.name
  visibility_timeout_seconds = var.visibility_timeout_seconds
  message_retention_seconds  = var.message_retention_seconds

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = var.max_receive_count
  })

  tags = var.tags
}

# Only the main queue may redrive into the DLQ.
resource "aws_sqs_queue_redrive_allow_policy" "dlq" {
  queue_url = aws_sqs_queue.dlq.id

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.main.arn]
  })
}

# Role the IoT rule assumes to deliver matched messages to the queue.
data "aws_iam_policy_document" "iot_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["iot.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "iot_rule" {
  name               = "${var.name}-iot-rule"
  assume_role_policy = data.aws_iam_policy_document.iot_assume.json
  tags               = var.tags
}

data "aws_iam_policy_document" "iot_send" {
  statement {
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.main.arn]
  }
}

resource "aws_iam_role_policy" "iot_send" {
  name   = "${var.name}-send"
  role   = aws_iam_role.iot_rule.id
  policy = data.aws_iam_policy_document.iot_send.json
}

# The IoT topic rule: matched MQTT messages are enqueued. For the presence
# heartbeat the SQL adds topic(2) of devices/{id}/telemetry as device_id —
# see modules/README.md for the exact invocation.
resource "aws_iot_topic_rule" "this" {
  name        = var.iot_rule_name
  enabled     = true
  sql         = var.iot_sql
  sql_version = "2016-03-23"

  sqs {
    queue_url  = aws_sqs_queue.main.id
    role_arn   = aws_iam_role.iot_rule.arn
    use_base64 = false
  }
}
