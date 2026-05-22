# Phase 1 Terraform modules

Reusable modules for the Control Plane. The Phase 1 root module (issue 01 —
VPC, ALB, RDS, Fargate cluster, S3 backend) composes these; they are kept
module-only here because the flat root in `infra/terraform/` is still the
Phase 0 single-device spike.

## `sqs-ingest`

One ingest topic: a main SQS queue, its dead-letter queue (with redrive
policy), and the IoT topic rule that feeds the queue. Reused per ingest
topic — presence heartbeats now, lifecycle events in #08.

The presence-heartbeat wiring (issue 07):

```hcl
module "presence_heartbeats" {
  source = "./modules/sqs-ingest"

  name          = "cp-presence-heartbeats"
  iot_rule_name = "presence_heartbeat"
  iot_sql       = "SELECT *, topic(2) as device_id FROM 'devices/+/telemetry'"
}
```

`topic(2)` is the `{id}` segment of `devices/{id}/telemetry`; the rule adds
it to the message as `device_id`, which `cp-ingest` reads.

## `cp-ingest-service`

The Fargate service running `cmd/cp-ingest` — the presence heartbeat
consumer (ADR-018). Cluster, subnets, security groups, and IAM roles are
inputs supplied by the Phase 1 root.

```hcl
module "cp_ingest" {
  source = "./modules/cp-ingest-service"

  image               = "<ecr-repo>:<tag>"
  region              = var.region
  cluster_arn         = module.cluster.arn
  subnet_ids          = module.network.private_subnet_ids
  security_group_ids  = [module.network.cp_ingest_sg_id]
  execution_role_arn  = module.iam.ecs_execution_role_arn
  task_role_arn       = module.iam.cp_ingest_task_role_arn
  heartbeat_queue_url = module.presence_heartbeats.queue_url
  heartbeat_dlq_url   = module.presence_heartbeats.dlq_url
  db_dsn_secret_arn   = module.secrets.db_dsn_arn
}
```

The `cp_ingest_task_role` must allow `sqs:ReceiveMessage`,
`sqs:DeleteMessage`, and `sqs:GetQueueAttributes` on the heartbeat queue
and `sqs:SendMessage` on its DLQ.
