# Issue 07 — Heartbeat ingest + online derivation

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 8–9, 31–33, § Implementation Decisions (Presence module, SQSConsumer[T] generic, schema, ingest worker shape).
- ADRs: ADR-011 (structured logs + correlation IDs), ADR-018 (Fargate workers for ingest).

## What to build

The first half of the presence model: the agent's 30s heartbeat flows through IoT Core → SQS → `cp-ingest` Fargate worker → Postgres `devices.last_seen`. `GET /devices/{id}` derives `is_online` from `last_seen` against the 90s online threshold. End-to-end demoable: the dev Mac's agent publishes a heartbeat, the API reports the device as online within seconds.

Scope:

- `Presence` deep module: in-memory state per device, `RecordHeartbeat(deviceID, at)`, transition emission semantics. Fake-clock-able. Sweeper + lifecycle event handling are deferred to #08 — this slice only covers the heartbeat → last-seen path.
- `SQSConsumer[T]` deep module: generic queue consumer with schema validation (`correlation_id` required per ADR-011), DLQ posture, structured logging, graceful shutdown.
- `PresenceIngester` handler: parses heartbeat envelope, calls `Presence.RecordHeartbeat`, also writes `last_seen` to Postgres so the API can read it without coupling to the in-memory `Presence` state.
- `cp-ingest` Fargate worker scaffold: `cmd/cp-ingest/main.go` composes `SQSConsumer[Heartbeat]` with `PresenceIngester`. Uses the shared structured-log library from #19.
- Terraform: SQS queue `cp-presence-heartbeats` with DLQ; IoT Rule `presence-heartbeat` matching `SELECT *, topic(2) as device_id FROM 'devices/+/telemetry'` targeting the queue; Fargate service definition for `cp-ingest`.
- API: `GET /devices/{id}` computes `is_online` from `last_seen` (within 90s = online). The threshold is a configured constant — sweeper logic that emits transitions on staleness lives in #08.

## Acceptance criteria

- [ ] An agent publishing a heartbeat to `devices/{id}/telemetry` results in `devices.last_seen` updated within 5 seconds (verified by integration test).
- [ ] `GET /devices/{id}` returns `is_online: true` if `last_seen` is within 90s of now, `false` otherwise.
- [ ] Malformed heartbeats (missing `correlation_id`, unparseable JSON, unknown device_id) land in the DLQ rather than crashing the consumer; an audit-log entry is written for each.
- [ ] `SQSConsumer[T]` unit tests cover: handler invoked with valid payload; malformed payload → DLQ; handler panic → message redelivered then DLQ'd; graceful shutdown drains in-flight messages within timeout.
- [ ] `Presence` unit tests cover: `RecordHeartbeat` updates internal state; concurrent heartbeats from different devices are isolated; fake-clock-based tests for transition emission (full transition coverage in #08).
- [ ] `cp-ingest` runs as a Fargate service with structured JSON logs flowing to CloudWatch Logs.

## Blocked by

- Issue 03 (`devices` table, HTTP API foundation).
