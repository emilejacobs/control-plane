# Issue 01 — `device_services` sweeper for stale rows

Status: ready-for-agent
Type: AFK
Estimate: 2–4 hr

## Parent

- Source: [Phase 2 slice 1 PRD](../../phase-2-service-status/PRD.md) noted this as deferred; [slice 1 issue 01 acceptance criteria](../../phase-2-service-status/issues/01-end-to-end-mvp.md) explicitly says rows-not-in-this-report stay until "the sweeper's job in a later slice if it becomes needed".
- ADRs to honour: ADR-018 (Fargate workers — sweeper is a goroutine in `cp-ingest`, mirrors `internal/cp/ingest/sweeper.go` shape), ADR-012 (test policy).

## What to build

A goroutine in `cp-ingest` that periodically removes rows from `device_services` whose `(device_id, service_name)` is no longer in the agent's effective allow-list. Today (post-slice-2), an operator who **removes** a service from a device's allow-list via the dashboard sees the removed service's row linger in `device_services` indefinitely because `RecordServiceStates` does per-service UPSERT, not replace-all-per-device.

### The cheap path

`RecordServiceStates` could be changed to replace-all-per-device in one transaction: `DELETE FROM device_services WHERE device_id = $1 AND service_name NOT IN (...)` before the UPSERTs. This avoids a sweeper entirely. But: a transient mid-report ingest failure would leave the device with NO rows for ~5 min (until the next report). The sweeper path is slower-but-decoupled.

### The sweeper path (recommended)

- Goroutine in `cp-ingest` that runs every ~10 min.
- For each `(device_id, service_name)` row, if `last_reported < now() - 15 min`, delete it. 15 min = 3× the default 5-min cadence; gives slack for one transient miss.
- Operationally, a "removed" service stops being reported on the next agent tick → its row's `last_reported` stops advancing → after 15 min, sweeper deletes it.
- A device that goes OFFLINE for an hour returns; service rows are gone; first post-reconnect report restores them. Trade-off: brief gap in the dashboard's Services panel during offline windows >15 min. Acceptable.

### Scope

- New `internal/cp/ingest/device_services_sweeper.go` (mirrors `sweeper.go` for presence).
- New `Registry.DeleteStaleDeviceServices(ctx, threshold time.Duration) (deletedCount int, err error)` that runs the DELETE in one statement.
- Wire goroutine in `cmd/cp-ingest/main.go` alongside the existing presence sweeper.
- Integration test in `tests/integration/` covering: stale rows deleted, fresh rows preserved, sweeper handles empty DB.

## Acceptance criteria

- [ ] Sweeper deletes rows whose `last_reported < now() - 15 min`.
- [ ] Fresh rows (within 15 min) are preserved.
- [ ] Operator who removes a service from the allow-list via the dashboard sees the row disappear from the Services panel within ~20 min (worst case = next 10-min sweeper cycle + 15-min threshold).
- [ ] No DLQ growth; no ingest regressions.
- [ ] **Documentation updated.** Add a line to [architecture.md § Phase 2 slice 1 paragraph](../../../docs/architecture.md) explaining the sweeper; update [CONTEXT.md `device_services`](../../../docs/CONTEXT.md) glossary entry to mention the sweep semantics.

## Blocked by

- None.

## Decisions to record at implementation

- **Sweeper cadence + threshold.** Defaults proposed: 10 min cadence, 15 min stale threshold. Configurable via env vars (`DEVICE_SERVICES_SWEEP_INTERVAL`, `DEVICE_SERVICES_STALE_THRESHOLD`) with sane defaults. If operations wants faster removal, they tune via env.
- **Alarm on unexpected mass deletes?** A sweeper that deletes 100+ rows in one cycle is probably a sign of fleet-wide silence, not normal hygiene. Consider a slog warn line + a metric alarm. Defer until the basic sweeper is running clean for a few weeks.
