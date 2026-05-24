# PRD — Per-device service allow-list overrides

**Phase:** 2 (second slice — extends [service-status MVP](../phase-2-service-status/PRD.md))
**Started:** 2026-05-24
**Status:** in design

## Why

Slice 1 ships a fixed allow-list (`com.uknomi.webui`, `com.tailscale.tailscaled`) baked into [`mac-mini-rollout/modules/11-cp-agent.sh`](../../../mac-mini-rollout/modules/11-cp-agent.sh). Devices that run add-on workloads (anydesk, transcriber, raven, plate-recognizer) get no visibility on those; module-11's own comment already anticipates this slice ("per-device opt-in via a future overrides slice").

Operators need to tell a single device "also track service X" or "drop service Y" without re-running the install module and without SSH. Slice 1 surfaces the gap; this slice closes it.

This slice also establishes the **first downward CP→agent message pattern**. Slice 1 was unidirectional (agent→CP). The signed-command pipeline lands in Phase 3 (ADR-013); this slice carves out a narrow, unsigned precursor for a single config knob. The Phase 3 signed envelope will wrap, not replace, the message shape established here. See [ADR-028](../../docs/adr/0028-unsigned-config-update-phase-2.md) for the deferral.

## Scope of this PRD

Per-device override of the service-status allow-list only. Cadence override (`service_status_interval`) is in scope as a free freebie — same plumbing, two fields. Anything else config-shaped (broker URL, telemetry interval, cert paths) is explicitly out.

## The shape

```
operator edits allow-list in dashboard modal
  → POST /devices/{id}/service-allow-list  (CP-API persists override)
  → CP publishes `config.update` cmd to devices/{id}/cmd        ← downward channel
  → agent persists to /var/uknomi/agent-config.json (atomic write)
  → agent hot-reloads the ServiceStatusPublisher (rebuild in place)
  → agent ACKs on devices/{id}/cmd-result        ← reuses Phase-0 result topic
  → CP records ack timestamp; dashboard shows "applied"
  → next service-status cycle reports per the new list (visible in Services panel)
```

The downward channel reuses the **existing** `devices/{id}/cmd` topic + dispatcher + `cmd-result` topic. No new infra topics, no new SQS queue (the IoT Rule that pulls cmd-results into SQS is already in place for Phase 0's command spike). The new pieces are: one dispatcher handler in the agent, one API endpoint + storage column on the CP, one cmd-result consumer extension, and one dashboard modal.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Push vs pull | **Push via `devices/{id}/cmd`** | Channel exists end-to-end (dispatcher + result topic) since Phase 0. Pull would mean a new endpoint, an interval choice, and a chicken-and-egg with offline devices. Push reuses what's there; Phase 3 will inherit the same shape. |
| Signed vs unsigned | **Unsigned for slice 2; signing in Phase 3** | Signing is multi-day work (Ed25519 KMS key, agent-side verify, key rotation). Blast radius of unsigned `config.update` is bounded — worst case forces a different *reporting* allow-list, not service control. Recorded in ADR-028. |
| Storage | **JSONB column on `devices`** (`service_allow_list_override jsonb`, nullable) | One knob today; promote to a `device_config` table only when there's >1 knob and joins matter. Avoids a migration churn now. |
| Hot-reload | **In-process rebuild** of the `ServiceStatusPublisher` | Restart-via-launchd costs an MQTT reconnect and breaks the cmd-result ACK flow. In-process is ~30 lines + a mutex on the publisher's allow-list. |
| Override semantics | **Full effective list** stored, not deltas | Deltas need a known default to subtract from; the default lives in [`mac-mini-rollout/modules/11-cp-agent.sh`](../../../mac-mini-rollout/modules/11-cp-agent.sh) and the agent has no notion of it. Operators think in lists, not patches. |
| Dashboard surface | **Modal opened from the Services-panel header** | Inline-edit on the device page would turn slice 1's read-only status panel into a form. Modal keeps the read view clean and gives space for the "current default is …" hint. |
| Default discovery | **Agent reports current effective list** in every service-status payload | Lets CP display "(default)" vs "(overridden)" without duplicating the default per-OS bundle into the CP. Install module stays the source of truth for the default. |
| Persistence ordering on agent | **Write-then-ACK, atomic file replace** | Crash between write and ACK ⇒ next reboot picks up the new list; CP retries (next operator save) reconcile. Crash before write ⇒ CP retries on next operator save. No partial state. |

## Out of scope (later slices or phases)

- **Signing the downward channel** — deferred to Phase 3, recorded in [ADR-028](../../docs/adr/0028-unsigned-config-update-phase-2.md).
- **Other config knobs** (broker URL, telemetry interval, cert paths) — slice 2 only handles `service_allow_list` + `service_status_interval`.
- **Fleet-wide bulk edit** ("apply this list to every Mac at site X") — slice 2 is per-device. Bulk is trivially built on top.
- **Default discovery from the CP** — agent reports the live list; we don't ask the install module to teach the CP its defaults.
- **Edit history / undo** — the override column stores the current value; no audit log of past values in this slice (the standard ops audit log already captures *who* changed *what* via the API).

## Constraints / honoured ADRs

- **ADR-011** (correlation IDs): `config.update` envelope already carries `correlation_id`; ACK echoes it; CP stamps it on the audit-log row.
- **ADR-012** (test policy): integration tests for the new endpoint + the new cmd-result handler; agent unit tests for hot-reload + atomic write.
- **ADR-013** (Phase 3 signed pipeline): ADR-028 records the narrow Phase 2 carve-out and the Phase 3 wrap-not-replace plan.
- **ADR-018** (Fargate workers): cmd-result handling extends the existing `cp-ingest` consumer; no new container.
- **ADR-019** (Goose migrations): single migration `012_devices_service_allow_list_override.sql`.
- **ADR-021** (CloudWatch): no new alarm for slice 2. Existing `uknomi-cp-service-stopped` covers the operational risk. A `cmd-result.config-update.error` log filter can be added later if rollout proves noisy.

## Open questions

- **API shape: PUT vs POST.** Mutating a single device's override leans PUT (full replacement of the override resource). Going with PUT for now. POST would be needed if we ever support partial / delta edits — we're explicitly not.
- **Effective-list display when offline.** If the device is offline when the operator saves, the override is persisted CP-side but not yet ACK'd. The dashboard shows "pending" until the next cmd-result lands. Acceptable for slice 2; a "queued for delivery" badge is the right UI.
- **Override clearing.** Setting the override to `null` (or empty list?) should revert to the agent's bundled default. Going with: API accepts `null` to clear; empty list `[]` means "track nothing" (different from clear). Agent receives `null` as "use the in-file allow-list as shipped by module 11" — i.e. don't overwrite. This needs care at the write-to-disk step.

## Success criteria

- An operator can open a device's page, click "Edit tracked services", add or remove a service name, save, and within one service-status cycle (≤5 min default) see the updated list on the Services panel — without touching the device.
- A subsequent `terraform destroy` + re-`apply` of the device's install (re-running module 11) **does not** lose the override (override lives in the CP, agent fetches on enrollment or via next cmd).
- The new `config.update` command path round-trips with `correlation_id` end-to-end (operator → API → MQTT cmd → agent → MQTT cmd-result → API → audit log row).
- The Services panel distinguishes default-list rows from overridden rows visually.

## Refinements expected mid-implementation

- **Enrollment hand-off.** A fresh device enrolling for the first time needs to see any existing override on its CP record. Cleanest path: enrollment response (`POST /enrollments/...`) includes the override in the returned config blob, written into `agent-config.json` at install. Avoids a "first cmd after enrollment is the config sync" dance. Confirm against [`internal/cp/api/handlers/enrollment/enrollment.go`](../../internal/cp/api/handlers/enrollment/enrollment.go) at implementation time.
- **Atomic file write nuances on macOS.** `os.Rename` over an existing file is atomic on the same filesystem; `agent-config.json` lives in `/var/uknomi/`, all on the boot volume. No tmpfile-on-different-volume gotcha. Note for the implementing agent.
