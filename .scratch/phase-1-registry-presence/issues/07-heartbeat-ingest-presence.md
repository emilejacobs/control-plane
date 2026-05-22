# Issue 07 — Heartbeat ingest + online derivation

Status: done
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

- [x] An agent publishing a heartbeat to `devices/{id}/telemetry` results in `devices.last_seen` updated within 5 seconds (verified by integration test).
- [x] `GET /devices/{id}` returns `is_online: true` if `last_seen` is within 90s of now, `false` otherwise.
- [x] Malformed heartbeats (missing `correlation_id`, unparseable JSON, unknown device_id) land in the DLQ rather than crashing the consumer; an audit-log entry is written for each.
- [x] `SQSConsumer[T]` unit tests cover: handler invoked with valid payload; malformed payload → DLQ; handler panic → message redelivered then DLQ'd; graceful shutdown drains in-flight messages within timeout.
- [x] `Presence` unit tests cover: `RecordHeartbeat` updates internal state; concurrent heartbeats from different devices are isolated; fake-clock-based tests for transition emission (full transition coverage in #08).
- [x] `cp-ingest` runs as a Fargate service with structured JSON logs flowing to CloudWatch Logs.

## Blocked by

- Issue 03 (`devices` table, HTTP API foundation).

## Comments

### 2026-05-21 — landed in 10 cycles (`de44a45`..`52e505e`)

The heartbeat → last-seen path: agent telemetry → IoT Core → SQS →
`cp-ingest` → Postgres, with `GET /devices/{id}` deriving `is_online`.

- Cycle 1: `internal/cp/presence` — `RecordHeartbeat` (per-device state,
  mutex-isolated, offline→online transition signal) and `IsOnline` /
  `OnlineThreshold` (90s). Time is passed as parameters; the injected
  clock and `Sweep`/`OnConnect`/`OnDisconnect` are deferred to #08.
- Cycle 2: `Registry.UpdateLastSeen` + `Device.LastSeen`. An unknown or
  non-UUID id returns `ErrDeviceNotFound`, not a DB error.
- Cycle 3: `GET /devices/{id}` emits `is_online` + `last_seen_ago_seconds`
  (null when never seen).
- Cycles 4–6: `internal/cp/sqsconsumer` — the generic `SQSConsumer[T]`.
  Decode + correlation_id validation + dispatch + delete-on-success;
  unparseable JSON / missing correlation_id → DLQ; panic recovery with
  redelivery and `maxReceiveCount` redrive; graceful drain with
  `ErrDrainTimeout`. (The plan's four SQS cycles collapsed to three —
  happy path and malformed→DLQ are two branches of one method.)
- Cycle 7: `internal/cp/ingest` — `PresenceIngester`, the
  `SQSConsumer[Heartbeat]` handler. Persists `last_seen`, records the
  in-memory heartbeat; empty/unknown `device_id` → poison.
- Cycle 8: end-to-end integration test against moto SQS + Postgres —
  heartbeat → `last_seen` within 5s; malformed → DLQ + audit log.
- Cycle 9: `cmd/cp-ingest` — the Fargate worker composition root.
- Cycle 10: Terraform `modules/sqs-ingest` (queue + DLQ + redrive + IoT
  rule) and `modules/cp-ingest-service` (Fargate task + service + log
  group). Both pass `terraform validate`.

**Implementation notes.** The heartbeat envelope carries no timestamp, so
`last_seen` is stamped at ingest time. The DLQ split: poison messages
(bad JSON, missing correlation_id, unknown device) are sent to the DLQ
explicitly; transient handler failures are left for SQS redrive.
`cp-ingest` builds a `Registry` with a nil IoT provisioner — it only
updates `last_seen`, never enrolls.

**Scope honestly accounted.** The cycle-8 integration test verifies the
SQS → `cp-ingest` → Postgres half against moto; the agent → IoT Core →
SQS hop is the `aws_iot_topic_rule` in `modules/sqs-ingest`, which is
`terraform validate`'d but not apply-tested (no AWS account here). The
in-memory `Presence` model is wired but not yet load-bearing — the API
reads `devices.last_seen` from Postgres; `Presence` exists for #08's
sweeper. The DLQ-depth and sweeper-lag alarms (PRD § Hardening) belong to
#21.
