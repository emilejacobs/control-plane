# PRD — Service-status reporting

**Phase:** 2 (first slice)
**Started:** 2026-05-24
**Status:** in design

## Why

Operators today have no in-CP view of which services are running on each device. To answer "is the Edge UI up?" or "is nginx down on the Phoenix Mac?" they SSH in. Phase 2's stated objective is "replace 'SSH to check what's happening' with the dashboard"; service-status is the keystone — the rest of Phase 2 (log tail, Edge UI proxy, camera snapshot) gets built on the same agent-reports-to-CP pattern this lands.

This slice also sets the pattern Phase 3's command pipeline will inherit (`service.restart`, `run-script` results, etc.). Anything load-bearing here re-applies later; anything weird here gets re-litigated. Worth getting right.

## Scope of this PRD

Just service-status reporting end-to-end. Log tail, Edge UI proxy, and camera snapshot are separate slices and will get their own PRDs.

## The shape

```
agent collector → MQTT publish (devices/{id}/service-status)
  → IoT Rule (filter + correlation_id pass-through)
  → SQS queue (with DLQ)
  → cp-ingest handler (sqsconsumer.Consumer[T] pattern, mirror of heartbeat)
  → Postgres UPSERT into device_services
  → GET /devices/{id}/services API
  → dashboard per-device Services panel (TanStack Query poll)
  → CloudWatch alarm on failed-service count > 0
```

Pipeline mirrors heartbeat (`internal/cp/ingest/heartbeat.go` + `infra/terraform-deploy/cp-ingest.tf`); see `internal/cp/ingest/heartbeat.go:38-75` for the handler shape and `infra/terraform/modules/sqs-ingest/main.tf:79-90` for the IoT Rule wiring pattern.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Cadence | **5-min periodic push** | Uniform with heartbeat pattern; bandwidth budget bounded; sweeper-as-backstop trivial (no fresh row in N min → mark `unknown`). Push-on-change is a Phase 4 optimisation; first land the simple loop. |
| What gets reported | **Config-driven allow-list** in the agent | Bounded payload; predictable schema; no surprise from enumerating hundreds of system services. Default per-OS bundle ships with the agent; future slice can add per-device overrides via dashboard. |
| Storage | **Separate `device_services` table**, PK `(device_id, service_name)` | Lets us answer cross-device queries cleanly ("which devices have nginx down?") for Phase 2 fleet view; JSONB-on-devices would push that into application code. |
| Topic + queue | **Separate from heartbeat** (`devices/+/service-status` → `uknomi-cp-service-status`) | Different cadence (5 min vs 30 s), different schema, different handler. Mixing into heartbeat couples two slow-vs-fast loops; ADR-018 covers the Fargate-not-Lambda math for the extra throughput. |
| Slice 1 scope | **Full vertical slice** — backend + dashboard panel + alarm | Operator value lands in one slice rather than three. Avoids the "backend built, no UI for a week" gap. |

## Out of scope (later slices)

- **Per-device service-list overrides via dashboard.** Default per-OS allow-list ships with the agent for slice 1; runtime edits later.
- **Service control commands** (`service.restart`, `start`, `stop`) — Phase 3. The agent already has the `service.status` *command* handler; Phase 3 adds the others.
- **Push-on-change cadence** — Phase 4 optimisation.
- **Log tail / Edge UI proxy / camera snapshot** — separate Phase 2 slices, separate PRDs.

## Constraints / honoured ADRs

- ADR-011: structured slog + `correlation_id` stamped end-to-end; payload schema requires `correlation_id` (mirroring the heartbeat schema; cp-ingest's `sqsconsumer.Consumer[T]` rejects DLQ-bound on missing).
- ADR-012: integration test for the new endpoint; idempotency-on-UPSERT for the cp-ingest handler.
- ADR-018: Fargate worker pattern (no Lambda) — extends `cp-ingest` with the new handler, doesn't introduce a new container.
- ADR-019: schema migration via Goose, embedded under `internal/cp/storage/migrations/`.
- ADR-021: alarm wires through the existing `uknomi-cp-alarms` SNS topic + per-alarm runbook under `docs/runbooks/alarms/`.

## Open questions

- **Initial default allow-list per OS.** Need to look at what's actually running on the Wave 0 bench Mac to pick a starting set. Talk to operations / read mac-mini-rollout install scripts for the canonical service set. Tracked in issue 01's blockers.
- **Alarm threshold.** Slice 1 alarm fires when the count of `stopped` services in the last 15-min window > 0 (see state-vocabulary note below). Refine after operating for a few weeks.

## Refinements surfaced mid-implementation

- **State vocabulary tightened to `running | stopped | unknown`** (was: + `failed`). Agent's existing `service.Backend` (`internal/service/backend_{darwin,linux}.go`) doesn't distinguish "intentionally stopped" from "failed" in a single launchctl/systemctl call — both look the same. Adding `StateFailed` would require parsing launchd plists / systemctl `--state=failed` output, which is multi-day work that belongs in Phase 3 alongside service-control commands (which already need to read exit codes).
  - **Alarm implication:** fires on `stopped` not `failed`, with the known false-positive risk on operator-initiated stops. Acceptable for slice 1.
  - **Schema implication:** `device_services.state` column is `text` (not an enum) so the future `failed` value lands without a migration when Phase 3 extends the backend.

## Success criteria for the PRD as a whole (all slices)

- Operators can see, on each device's page, the state of each tracked service updated within ~5 min.
- "Service nginx is failed on > 1 devices for > 15 min" pages on Slack via the existing alarm SNS.
- A future cross-fleet view ("show me all devices where com.uknomi.edge-ui isn't running") needs only a SQL query + a route — no agent changes.
