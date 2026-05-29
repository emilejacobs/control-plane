# Phase 1 Terraform modules

Reusable modules for the Control Plane. The Phase 1 root module (issue 01 —
VPC, ALB, RDS, Fargate cluster, S3 backend) composes these; they are kept
module-only here because the flat root in `infra/terraform/` is still the
Phase 0 single-device spike.

## `sqs-ingest`

One ingest topic: a main SQS queue, its dead-letter queue (with redrive
policy), and the IoT topic rule that feeds the queue. Reused per ingest
topic — presence heartbeats and lifecycle events.

> ⚠️ **When you add a new agent→CP publish topic** (`devices/{id}/<topic>`),
> you must ALSO add that topic to the device IoT policy's `iot:Publish`
> resource list in [`../policy.tf`](../policy.tf) (`UknomiAgentPolicy`).
> AWS IoT **silently drops** publishes to topics the cert isn't authorized
> for — the broker still PUBACKs, so the agent looks perfectly healthy
> (heartbeats on `/telemetry` keep flowing) while cp-ingest sees *nothing*.
> This trap bit the service-status rollout and again the health-probes
> rollout (#19). A new `sqs-ingest` module instance is only half the wiring;
> the policy line is the other half.

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

The presence-lifecycle wiring (issue 08):

```hcl
module "presence_lifecycle" {
  source = "./modules/sqs-ingest"

  name          = "cp-presence-lifecycle"
  iot_rule_name = "presence_lifecycle"
  iot_sql       = "SELECT *, newuuid() as correlation_id FROM '$aws/events/presence/+/+'"
}
```

`$aws/events/presence/+/+` matches IoT Core's `connected`/`disconnected`
events; their payload already carries `clientId` and `eventType`. AWS
lifecycle events have no `correlation_id`, so the rule mints one with
`newuuid()` — `SQSConsumer[T]` requires it per ADR-011.

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
  lifecycle_queue_url = module.presence_lifecycle.queue_url
  lifecycle_dlq_url   = module.presence_lifecycle.dlq_url
  db_dsn_secret_arn   = module.secrets.db_dsn_arn
}
```

The `cp_ingest_task_role` must allow `sqs:ReceiveMessage`,
`sqs:DeleteMessage`, and `sqs:GetQueueAttributes` on both the heartbeat and
lifecycle queues, and `sqs:SendMessage` on each of their DLQs.
