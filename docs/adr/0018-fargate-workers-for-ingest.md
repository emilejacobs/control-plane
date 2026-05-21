# ADR-018: Fargate workers (not Lambda) for all MQTT-side ingest, all phases

**Status:** Accepted (2026-05-21)

**Context.** Devices publish to IoT Core topics: heartbeat + telemetry in Phase 1, service-status reports in Phase 2, command-results + agent-self-update reports in Phase 3. Bulk data (camera snapshots, large log payloads beyond MQTT-friendly sizes) goes through the Tailscale subnet router per ADR-003 and is not in scope here.

Two canonical AWS patterns for IoT-side ingest:

1. **Lambda**: IoT Rule targets a Lambda function directly. Concurrent execution model, per-invocation pricing, AWS-managed scaling.
2. **Fargate consumer**: IoT Rule targets SQS; a long-running Fargate task consumes the queue.

The throughput math across all phases is bounded:

- Phase 1 steady-state: 25 Macs × 2 heartbeats/min = ~50 events/min.
- Phase 2 add-on: service-status reports at similar cadence, ~100 events/min total.
- Phase 3 add-on: command-results (bursty but bounded by command issuance rate, which is operator-driven and small) + self-update version reports, ~200 events/min ceiling under realistic operator load.

This is four orders of magnitude below where Lambda's scaling story earns its complexity.

The factors that drove the decision are not throughput; they are paradigm cost across the codebase.

**Decision.** All MQTT-side ingest runs as Fargate worker tasks, written in Go, using the same container patterns as the API service. IoT Rules target SQS queues; Fargate consumers read from SQS. Each ingest concern (presence, command-results, etc.) is a goroutine within one or more Fargate tasks; new ingest concerns added in later phases are new goroutines or new tasks, not a paradigm switch.

Phase 1 concrete shape:

- One Fargate service `cp-ingest` (0.25 vCPU / 0.5 GB to start; scale up if needed).
- IoT Rule `presence-heartbeat` matches `SELECT *, topic(2) as device_id FROM 'devices/+/telemetry'` and targets SQS queue `cp-presence-heartbeats`.
- A `PresenceIngester` goroutine in `cp-ingest` polls the queue, validates payloads against the schema (correlation_id required per ADR-011), updates `devices.last_seen` in Postgres via a persistent connection pool.
- A `PresenceSweeper` goroutine in the same task runs `time.NewTicker(30*time.Second)`; on each tick, marks devices with `last_seen > 90s ago` as offline (the second leg of the Q2 presence definition from the 2026-05-21 grilling: heartbeat staleness is the source of truth, MQTT lifecycle is the online → offline fast-path, the sweeper closes the gap when IoT keepalive hasn't fired yet).
- A `LifecycleIngester` goroutine subscribes to IoT lifecycle events via a second SQS queue fed by an IoT Rule on `$aws/events/presence/connected/+` and `disconnected/+`, flipping `last_seen` and emitting state transitions immediately.
- DLQ on both SQS queues; alarm on DLQ depth > 0.

Phase 2+ additions are goroutines or sibling Fargate services using the same pattern. No Lambda is introduced.

**Consequences.**

- (+) **One paradigm across the codebase.** API service and ingest workers are both Go-in-containers on Fargate. One deployment pipeline shape, one local-dev story (`go run` or `docker compose`), one observability surface (CloudWatch Logs + structured `slog` per ADR-011), one debugging model (`docker exec`, real shells, real `/proc`).
- (+) **AI-agent-development friendliness.** With architectural-reviewer-only humans, agents benefit substantially from uniform patterns. Container behavior is the same locally and deployed; Lambda's local-vs-deployed dual mode is a recurring source of agent confusion.
- (+) **Persistent Postgres connection pool.** No RDS Proxy needed; Fargate task holds the pool across all events.
- (+) **Sweeper-as-goroutine is direct.** A 30s ticker in the same process that does ingest is simpler than EventBridge schedule → Lambda → DB poll.
- (+) **Shared libraries trivially.** Device model, schema validators, log setup are the same Go packages used by the API service. Lambda + Fargate would have required either monorepo discipline plus packaging gymnastics or two parallel codebases.
- (-) **~$8-15/month for a near-idle task** when Lambda would charge cents at this throughput. Accepted as a paradigm-parsimony purchase.
- (-) **SQS queue + Fargate task is two infra components per ingest path** vs Lambda's "just the function." But SQS is zero-touch infrastructure; the operational delta is marginal. Net infra is comparable once you count Lambda's VPC attachment, DLQ, and scheduled triggers.
- (-) **Idiomatic-AWS-docs path is Lambda for IoT-Rule-to-handler.** Future engineers familiar with AWS reference architectures may initially expect Lambda. The ADR exists to make the deviation findable.

**Verification.** TBD — added at implementation. Integration tests cover:

- `tests/integration/presence_test.go::TestHeartbeatUpdatesLastSeen` — publishing to the `devices/+/telemetry` topic via the test IoT endpoint results in a `last_seen` update within 5s.
- `tests/integration/presence_test.go::TestSweeperFlipsStaleDevicesOffline` — a device with `last_seen` set 91s in the past is marked offline within 30s by the sweeper tick.
- `tests/integration/presence_test.go::TestLifecycleDisconnectFlipsImmediately` — a simulated IoT `disconnected` event flips the device to offline within 5s without waiting for the sweeper.
- `tests/integration/presence_test.go::TestPresenceDLQOnInvalidPayload` — a malformed heartbeat lands in the DLQ rather than crashing the consumer.
