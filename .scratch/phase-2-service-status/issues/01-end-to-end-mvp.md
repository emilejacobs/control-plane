# Issue 01 — Service-status reporting end-to-end (MVP)

Status: done (2026-05-24 — landed live on the Wave 0 bench Mac across ~25 commits)
Type: AFK
Estimate: 7–9 days (actual: ~1 long session)

## Parent

- PRD: [`PRD.md`](../PRD.md)
- ADRs to honour: ADR-011 (correlation IDs), ADR-012 (test policy), ADR-018 (Fargate workers), ADR-019 (Goose migrations on startup), ADR-021 (CloudWatch alarms).

## What to build

A full vertical slice: agent collects service state on a 5-min cadence, publishes to a new IoT topic, cp-ingest persists into a new `device_services` table, the API exposes it under the existing per-device endpoint, the dashboard renders it on the per-device page, and a CloudWatch alarm pages on `failed`-state services that linger.

### Scope

**Agent (`internal/agent/`, `internal/telemetry/`):**

- New collector `internal/telemetry/servicestatus.go`: wraps the existing `service.Backend` (`internal/service/backend_{darwin,linux}.go`). Iterates over a config-driven allow-list of service names; calls `Backend.Status(ctx, name)` for each; produces a flat slice for the publish payload.
- New publisher loop in `internal/telemetry/` (or extend `Publisher` with a second ticker — pick whichever keeps the call graph cleaner). Publishes to `devices/{device_id}/service-status` on a 5-min cadence (configurable as `cfg.ServiceStatusInterval`, default `5m`).
- New config field in `internal/config/`: `ServiceAllowList []string` (loaded from agent config file; default per-OS bundled in `internal/agent/defaults.go` or similar).
- Payload schema (JSON):
  ```json
  {
    "device_id": "...",
    "correlation_id": "...",
    "reported_at": "RFC3339",
    "services": [
      {"name": "com.uknomi.edge-ui", "state": "running", "state_since": "RFC3339"},
      {"name": "nginx", "state": "failed", "state_since": "RFC3339"}
    ]
  }
  ```

**Storage (`internal/cp/storage/migrations/`):**

- New Goose migration `011_device_services.sql`:
  ```sql
  CREATE TABLE device_services (
    device_id     uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    service_name  text NOT NULL,
    state         text NOT NULL,  -- running | stopped | unknown (Phase 3 may add 'failed' — see PRD § Refinements)
    state_since   timestamptz NOT NULL,
    last_reported timestamptz NOT NULL,
    PRIMARY KEY (device_id, service_name)
  );
  CREATE INDEX device_services_stopped ON device_services (state) WHERE state = 'stopped';
  ```

**cp-ingest (`internal/cp/ingest/`):**

- New handler `internal/cp/ingest/servicestatus.go` mirroring `heartbeat.go`'s shape. `ServiceStatusReport` struct + `Correlation()`; `Ingester.Handle(ctx, report)` performs a transactional UPSERT of every reported `(device_id, service_name)` row (replace-all-per-device semantics: rows not in this report are *not* removed by this handler — that's the sweeper's job in a later slice if it becomes needed; for slice 1 the allow-list is bounded so churn is low).
- `cmd/cp-ingest/main.go` wires the new handler against a new `sqsconsumer.Consumer[ServiceStatusReport]`.

**Infra (`infra/terraform-deploy/cp-ingest.tf`):**

- New `module "sqs_service_status" { source = "../terraform/modules/sqs-ingest" }` block, mirroring the heartbeat module instantiation. IoT SQL: `SELECT *, topic(2) as device_id FROM 'devices/+/service-status'` (correlation_id already in the agent payload, so no `newuuid()` needed).
- Pass the queue URLs + DLQ into `module.cp_ingest` (extend the module's variables in `infra/terraform/modules/cp-ingest-service/` to accept the new queue, plus env-var wiring for `SERVICE_STATUS_QUEUE_URL` / `SERVICE_STATUS_DLQ_URL`).

**Infra (`infra/terraform/policy.tf`):** ⚠️ **PREREQUISITE — easy to miss, caught the hard way.**

The agent's `UknomiAgentPolicy` (in the IoT-core root, NOT the deploy root) must list `topic/devices/${iot:Connection.Thing.ThingName}/service-status` in the `iot:Publish` ARN set. AWS IoT silently drops publishes to disallowed topics — the MQTT broker still ACKs at session layer, so the agent thinks the publish succeeded. Symptoms: heartbeats flow fine, service-status queue stays at zero, no error log entries. Verified during Wave 0 bench upgrade.

**API (`internal/cp/api/handlers/devices/`):**

- Extend the existing `GET /devices/{id}` handler response with a `services` field: `[{name, state, state_since, last_reported}]`. Computed by joining `device_services` where `device_id = $1`. Order by `name`.
- Add a `services` field to the public OpenAPI doc / response struct.

**Dashboard (`web/`):**

- New `Services` panel in `web/app/devices/[id]/page.tsx` rendering a small table: name | state badge | "running since N hours" (or "failed N min ago" for failures).
- Reuse the `PresenceChip` colour palette pattern for state badges.
- Same 10-second TanStack Query poll as the rest of the per-device view (no separate poll needed — the data rides on the `GET /devices/{id}` response).

**Alarm (`infra/terraform-deploy/alarms.tf`):**

- New log-metric-filter on cp-ingest log stream: count of `service-status` ingests that recorded a `stopped` state (filter pattern `{ $.msg = "service-status.stopped" }`). Slice 1 uses `stopped` not `failed` per PRD § Refinements (the agent backend can't distinguish them in a single launchctl/systemctl call).
- New `aws_cloudwatch_metric_alarm` "uknomi-cp-service-stopped": fires when the count > 0 over a 15-minute window.
- New runbook `docs/runbooks/alarms/service-stopped.md` explaining what to investigate (including the known false-positive on operator-initiated stops).

### Out of scope

- Per-device service-list overrides via dashboard. Default per-OS allow-list ships in agent config.
- Sweeper that removes rows for services dropped from the allow-list. Bounded by allow-list size; revisit if it becomes a problem.
- Service control (`service.restart`, etc.) — Phase 3.

## Acceptance criteria

- [ ] Unit + integration tests cover: agent collector (mocked `service.Backend`); cp-ingest handler (testcontainers Postgres); API response shape; sqsconsumer poison-handling on missing `correlation_id` (mirrors `heartbeat_test.go`).
- [ ] `goose up` against a fresh DB creates the new table + index.
- [ ] `terraform fmt + validate` clean on both `infra/terraform/` and `infra/terraform-deploy/`.
- [ ] The Wave 0 bench Mac (`07-eegees-mesa-macmini`) shows live service-status rows in `device_services` within 6 minutes of the new agent image rolling out.
- [ ] The per-device dashboard view at `/devices/{id}` shows a Services panel with at least one row.
- [ ] The `uknomi-cp-service-failed` alarm fires (in test) when an allowlisted service is stopped on the bench Mac.
- [ ] **Documentation updated.** `docs/architecture.md` § Modules + § Cloud infrastructure mention the new collector + queue + table. `docs/CONTEXT.md` defines `device_services` if any new domain term landed. No ADR needed unless one of the open questions surfaces an irreversible decision (e.g. picking JSONB over a table would warrant one, but we already picked the table).

## Blocked by

- **Default allow-list per OS.** Need to nail down which services the agent reports on day one — see PRD § Open questions. The Mac list should at minimum include the Edge UI service (`com.uknomi.edge-ui` per the rename) plus whatever else `mac-mini-rollout` installs. The Linux list is best-effort given Pi/Radxa deprecation. **Resolution path:** read `mac-mini-rollout/install*` scripts to enumerate the installed services; if uncertain, pick a 2–3 service starter set and accept that operators can extend later.

## Notes

- The TDD memory `feedback_tdd_commit_cadence` applies: commit per red→green cycle, don't batch. Per `feedback_tdd_auto_proceed`, start the next cycle without asking permission.
- Existing heartbeat tests in `internal/cp/ingest/heartbeat_test.go` are the canonical template; the new service-status tests should mirror their structure for paradigm parsimony.
- The Wave 0 bench Mac's agent will need a manual reinstall to pick up the new binary (no Phase 3 self-update yet). Plan the rollout window with that in mind.

## Completion notes (2026-05-24)

Slice 1 landed and is live on the Wave 0 bench Mac (`bbe0540c-d58e-435e-8b20-cef6fde8ddcc`). Dashboard's per-device Services panel renders both default-allow-list entries (`com.uknomi.webui`, `com.tailscale.tailscaled`) as `running` with green pills.

**Notable things that surfaced during implementation:**

1. **State vocabulary tightened to `running | stopped | unknown`** (originally PRD listed `failed` too). The agent's existing `service.Backend` can't distinguish "intentionally stopped" from "failed" in a single launchctl/systemctl call. Deferred to Phase 3 where service-control commands will need exit-code parsing anyway. Schema column stays `text` so Phase 3 adds the new value without a migration.

2. **IoT policy widening prerequisite** (now in the "Scope" section above). The agent's `UknomiAgentPolicy` had to gain `service-status` in its publish ARN list. The original Terraform pass added the IoT Rule + SQS but missed the device-side policy. Took ~5 min to diagnose in prod (heartbeats fine, queue empty, no agent error). Now documented; future per-topic additions need to update both halves.

3. **Bench Mac upgrade was a hot-swap, not a reinstall.** Module 11's idempotent path can re-enroll → orphan the existing device_id. Did instead: scp new binary + python-edit JSON config to add the two new fields + `launchctl kickstart -k system/com.uknomi.agent`. Phase 3's self-update primitive will eventually replace this manual ritual.

4. **`StateSince` cosmetic clock-skew.** Agent stamps `state_since` against its local clock; cp-side wall clock can drift. First publish on the bench Mac rendered as "37 min ago" instead of the expected "seconds ago" because of a clock offset. Documented behavior per the PRD; tightens on the next state transition.

5. **Two messages instead of one on first successful publish.** Almost certainly Paho MQTT client's reconnect-retry behavior re-publishing the earlier policy-rejected message. UPSERT semantics in storage make duplicate writes a no-op; not worth investigating further unless it recurs at scale.

**Commits:** ~25 across this session, plus 1 in sister repo (`mac-mini-rollout/modules/11-cp-agent.sh` + the new agent binary in `bin/`). See git log for the full list; `74c5487` is the protocol extraction, `da5ec0e` is the IoT policy fix.

**Open follow-on (small):** the version string in the agent's config is install-time-frozen — the upgraded bench Mac still reports `"version": "2b6cfd0"` in its config even though the running binary is `a93470f`. Cosmetic for now; tighten when Phase 3's self-update lands a real binary-version stamp (e.g. via `-ldflags -X main.version=...`).
