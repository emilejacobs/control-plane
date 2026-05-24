# Issue 01 — Per-device allow-list overrides end-to-end

Status: ready-for-agent
Type: AFK
Estimate: 4–6 days

## Parent

- PRD: [`PRD.md`](../PRD.md)
- ADRs to honour: ADR-011 (correlation IDs), ADR-012 (test policy), ADR-013 (Phase 3 signed pipeline — Phase 2 carve-out at ADR-028), ADR-018 (Fargate workers), ADR-019 (Goose migrations), [ADR-028](../../../docs/adr/0028-unsigned-config-update-phase-2.md) (unsigned `config.update`).

## What to build

A full vertical slice for editing the per-device service allow-list (and cadence) from the dashboard, pushed down to the agent via the existing `devices/{id}/cmd` channel, hot-reloaded in the running agent, ACK'd back via `cmd-result`, and reflected in the Services panel within one publish cycle.

### Scope

**Storage (`internal/cp/storage/migrations/`):**

- New Goose migration `012_devices_service_allow_list_override.sql`:
  ```sql
  ALTER TABLE devices
    ADD COLUMN service_allow_list_override jsonb,
    ADD COLUMN service_status_interval_override text;
  ```
  - `service_allow_list_override`: `null` = no override (agent uses its bundled list); JSON array of strings = effective list; `[]` = "track nothing".
  - `service_status_interval_override`: Go `time.ParseDuration` string (`"5m"`, `"30s"`). `null` = use agent default.

**Registry / repository (`internal/cp/registry/`):**

- New methods on `Registry`: `GetServiceConfig(ctx, deviceID)` and `SetServiceConfig(ctx, deviceID, allowList *[]string, interval *string)`. Returns/accepts the override pair; `*[]string` so `nil` vs `&[]string{}` round-trip cleanly.
- Integration test against real Postgres for round-trip including `nil` clears.

**API (`internal/cp/api/handlers/devices/`):**

- New handler `PUT /devices/{id}/service-config` accepting:
  ```json
  {
    "service_allow_list": ["com.uknomi.webui", "com.tailscale.tailscaled"],
    "service_status_interval": "5m"
  }
  ```
  - Both fields optional; missing field = clear that override (set DB column to `null`); explicit `null` = clear.
  - Validates: list elements are non-empty strings ≤ 256 chars; interval parses with `time.ParseDuration` and is between 30s and 1h.
  - Auth-gated by existing site-scope middleware.
  - On success: persists the override row AND publishes a `config.update` cmd envelope to `devices/{id}/cmd` (correlation_id from request header or freshly minted; same pattern as Phase-0 cmd-publisher).
  - Returns 202 (accepted) with the correlation_id so the dashboard can poll/wait for the ACK.
- Extend the existing `GET /devices/{id}` response with a `service_config` field:
  ```json
  {
    "service_config": {
      "allow_list_override": ["..."],          // null when no override
      "allow_list_effective": ["..."],         // last list the agent reported
      "interval_override": "5m",               // null when no override
      "interval_effective": "5m",              // last interval the agent reported
      "last_applied_at": "2026-05-25T...Z",    // last cmd-result ACK timestamp
      "last_applied_correlation_id": "..."
    }
  }
  ```

**cp-ingest cmd-result handler (new `internal/cp/ingest/cmdresult.go`):**

- New SQS consumer or extension to the existing one that pulls from `cmd-result` queue (slice 1's heartbeat module pattern — IoT Rule + SQS).
- On a `config.update` ACK: updates `devices.last_applied_at`, `devices.last_applied_correlation_id`. Failure ACKs (`success: false`) get logged at `Warn` and recorded with the error code; no DB write of last-applied.
- Idempotent on the (correlation_id, device_id) tuple — re-deliveries are no-ops.
- Infra: if the cmd-result queue doesn't yet exist in the deploy-root terraform (Phase 0 ran via the developer CLI), add it as a fresh `sqs-ingest` module instantiation. Mirror the heartbeat module wiring.

**Agent — config.update handler (`internal/handlers/configupdate/`):**

- New dispatcher handler `configupdate.New(cfg ConfigStore, publisher *telemetry.ServiceStatusPublisher)`. Registered alongside `heartbeat`, `service.status`, `service.restart` in [`internal/agent/agent.go`](../../../internal/agent/agent.go) (the dispatcher.Register call site).
- Handler payload schema:
  ```json
  {
    "service_allow_list": ["..."] | null,
    "service_status_interval": "5m" | null
  }
  ```
- Behavior:
  1. Validate (same rules as API: ≤256 chars per entry, interval ≤ 1h, etc.)
  2. Atomic write to `/var/uknomi/agent-config.json` (write to `agent-config.json.tmp`, `os.Rename` to target). Preserves `nil` semantics (don't overwrite the field when payload field is missing or null; do overwrite to `[]` when explicitly empty).
  3. Hot-reload `ServiceStatusPublisher`: new method `Publisher.SetAllowList(list []string)` + `SetInterval(d time.Duration)`. The publisher takes a mutex on the collector; next tick uses the new values.
  4. If the publisher was nil (override is the first time this agent gets any allow-list at all), construct it now and start it. Symmetric: if override clears the list to empty, stop the publisher loop.
  5. Returns `{"applied_at": "RFC3339", "effective_allow_list": [...], "effective_interval": "5m"}`. Dispatcher wraps in the standard `envelope.Result`.

**Agent — `ServiceStatusPublisher` hot-reload (`internal/telemetry/servicestatus_publisher.go`):**

- Add `mu sync.RWMutex` + `allowList []string` + `interval time.Duration` fields.
- `Run` reads fields under the lock per tick; `SetAllowList` / `SetInterval` take the write lock.
- If interval changes, reset the ticker (`ticker.Reset(d)`).
- Unit tests:
  - `SetAllowList` mid-flight changes what the next tick reports.
  - `SetInterval` reduces a 5m tick to a 30s tick observable in the test (compress via injectable clock).
  - Concurrent `Set` + tick is race-free (`-race` on the test).

**Agent — `ServiceStatusCollector` shared mutable allow-list:**

- The collector currently holds `AllowList []string` as a value. The publisher's `SetAllowList` needs to flow through. Cleanest: collector reads its list from a `func() []string` getter the publisher injects, or the publisher owns the list and passes it into `Collect(ctx, list)`.
- Pick the latter — keeps the collector stateless and the publisher the single source of truth.

**Agent — config.go schema:**

- The agent-config.json schema already has `service_allow_list` and `service_status_interval` (slice 1). Add a hidden field `service_allow_list_source` ∈ `{"install" | "override"}` purely for the agent startup-log (so a human SSH'd in can tell the override status). Optional, decide at implementation.

**Enrollment hand-off (`internal/cp/api/handlers/enrollment/enrollment.go`):**

- When a fresh enrollment lands, look up any pre-existing override for the new `device_id` (same Mac may have been overridden under a prior enrollment). If present, include in the enrollment-response config blob. The install module already JSON-parses the response into `agent-config.json`; this just adds two extra keys.
- Test: enrollment after an existing override returns the override in the blob.

**Dashboard (`web/`):**

- New `EditServicesModal` opened by a small "Edit" button on the Services panel header (`web/app/devices/[id]/page.tsx`).
- Modal contents:
  - Editable list of service names (add row, remove row, drag-reorder optional).
  - Hint line: "Default for this device's OS: `com.uknomi.webui`, `com.tailscale.tailscaled`" (sourced from the agent-reported effective list when there's no override; otherwise from the override row).
  - Interval input (free-form duration string, validated client-side).
  - Save → PUT, then polls `GET /devices/{id}` every 2s until `last_applied_correlation_id` matches the one returned by the PUT, then closes.
  - Cancel button.
- Services panel header gains a small "(overridden)" or "(default)" badge derived from `service_config.allow_list_override !== null`.
- Vitest coverage for modal logic (validation, save flow, polling).

**Infra (`infra/terraform-deploy/`):**

- Add the cmd-result SQS+IoT Rule pipeline (if not yet present in deploy-root). Mirror `module "sqs_heartbeats"` / `module "sqs_service_status"`. IoT SQL: `SELECT *, topic(2) as device_id FROM 'devices/+/cmd-result'`.
- Wire the new queue URLs into `module.cp_ingest` as `CMD_RESULT_QUEUE_URL` / `CMD_RESULT_DLQ_URL` env vars.

**Infra (`infra/terraform/policy.tf`):**

- ⚠️ The agent's `UknomiAgentPolicy` already has `iot:Publish` on `devices/${iot:Connection.Thing.ThingName}/cmd-result` (Phase 0). Confirm; if missing, add. Slice 1 made the same mistake the hard way for service-status — `policy.tf` lives in the IoT-core root, not the deploy root.
- Also confirm the policy permits `iot:Subscribe` + `iot:Receive` on `devices/${iot:Connection.Thing.ThingName}/cmd` — that's the existing cmd subscription path.

## Acceptance criteria

- [ ] Migration 012 runs cleanly on prod (auto-applied per ADR-019 on cp-api startup).
- [ ] `PUT /devices/{id}/service-config` accepts a valid payload, persists the override, publishes `config.update`, returns the correlation_id.
- [ ] Agent receives `config.update`, hot-reloads the publisher, ACKs on `cmd-result` with the same correlation_id; next service-status tick reflects the new list.
- [ ] cp-ingest cmd-result handler persists the ACK timestamp + correlation_id to the device row.
- [ ] `GET /devices/{id}` returns the override + effective + last-applied fields.
- [ ] Dashboard modal can edit the list, save, observe "applied" within ~5 s of network round-trip, and the Services panel shows the new rows on the next service-status cycle (≤5 min default).
- [ ] Override survives an agent process restart (override is on disk via the atomic write; agent reads it on next start) — verified end-to-end on the Wave 0 bench Mac.
- [ ] Override survives a CP-side reboot (override is in Postgres) — verified by re-loading the device page after restarting cp-api locally.
- [ ] An offline-at-save device receives the override on next reconnect (MQTT cmd retain on the broker side; need to confirm Phase 0's cmd channel uses QoS 1 + clean session = false; if not, add a "pending-config" reconcile job — defer that to a follow-up issue if needed).
- [ ] Enrollment response for a device with an existing CP-side override includes the override in the agent-config JSON returned to module 11.
- [ ] Integration tests: API endpoint, cmd-result ingest handler, enrollment override hand-off.
- [ ] Unit tests: agent dispatcher handler (validation, atomic write, hot-reload), publisher `SetAllowList` / `SetInterval` (with `-race`).
- [ ] Dashboard vitest: modal save + poll happy path; validation error path.
- [ ] **Documentation updated.** `docs/architecture.md` gains a short subsection under "Phase 2" describing the downward-config flow + the unsigned-channel carve-out (point at ADR-028). `docs/CONTEXT.md` gains "Service allow-list override" as a glossary entry. ADR-028 is created.

## Blocked by

- None. (cp-ingest cmd-result wiring may surface gaps in the deploy-root terraform left over from Phase 0; handle inline.)

## Open at start of implementation (decide and record)

- **MQTT cmd-channel retention semantics.** The QoS / clean-session settings on the agent's cmd-subscription decide whether an offline-at-save device gets the override on reconnect via broker buffering, or whether we need a `pending-config` reconcile loop. Investigate before writing the "offline operator save" acceptance test.
- **Audit log entry for `service-config.updated`.** Slice 1 audit log already captures device-touching writes. Confirm the new endpoint flows through the same middleware; add a row type if not.
