# Issue 08 — Sweeper + IoT lifecycle fast-path

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 10–11, § Implementation Decisions (PresenceSweeper, LifecycleIngester).
- ADR: ADR-018 (Fargate worker, sweeper as goroutine).

## What to build

The second half of the presence model: a `PresenceSweeper` goroutine in `cp-ingest` ticks every 30s and flips devices with `last_seen > 90s ago` to offline; a `LifecycleIngester` listens to IoT Core `$aws/events/presence/connected/+` and `disconnected/+` events for the fast-path online → offline transition. Together they close the freshness gap so presence is accurate within 60s even when an agent dies without sending a TCP FIN.

Scope:

- `Presence.Sweep(now)` returns `[]Transition` for devices whose `last_seen > 90s ago` and whose previous emitted state was not already offline. Idempotent transition emission (already-offline devices are not emitted twice).
- `Presence.OnConnect(deviceID, at)` and `Presence.OnDisconnect(deviceID, at)` — emit transitions only on state changes; a disconnect for a device already offline is a no-op; a connect for a device already online is a no-op.
- `PresenceSweeper`: goroutine in `cp-ingest`. `time.NewTicker(30*time.Second)`. On tick, calls `Presence.Sweep(time.Now())` and persists transitions (update `devices.last_seen_state` or equivalent) and writes audit-log entries.
- `LifecycleIngester`: SQS consumer subscribing to `cp-presence-lifecycle`. Maps IoT lifecycle messages to `Presence.OnConnect`/`OnDisconnect`. Persists immediately so the API reflects the change within a poll cycle.
- Terraform: second SQS queue `cp-presence-lifecycle` with DLQ; IoT Rules for `$aws/events/presence/connected/+` and `disconnected/+` targeting it.
- Tests build out the seven `Presence` behaviors enumerated in the grilling session: stale device emitted by sweep; fresh device not emitted; sweep idempotent across successive calls; disconnect emits immediately; reconnect emits online; connect on already-online no-op; threshold configurable at construction.

## Acceptance criteria

- [x] A device with `last_seen` set 91s in the past is marked offline by the sweeper within 30s.
- [x] A simulated IoT `disconnected` event flips the device to offline within 5s without waiting for the sweeper.
- [x] Successive sweeper ticks do not emit duplicate transitions for already-offline devices.
- [x] A reconnecting device with a fresh heartbeat is marked online and an online transition is emitted.
- [ ] Pulling power on the dev Mac (Wave-0 manual verification) results in the device showing offline in the API within 60s.
- [x] All seven `Presence` behaviors from the PRD's testing decisions section are covered by unit tests with a fake clock.
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 07.

## Comments

### 2026-05-21 — landed in 10 cycles (`d6f0b59`..`a35b07a`)

The second half of the presence model: stale-device sweep + the IoT
lifecycle fast-path.

- Cycle 1: `Presence.Sweep`, `OnConnect`, `OnDisconnect`, and a
  constructor threshold option — the seven behaviors, unit-tested with
  time as a parameter.
- Cycle 2: migration `005` (`devices.is_online`, `presence_changed_at`)
  + `registry.SetPresence`.
- Cycle 3: `GET /devices/{id}` reads the stored `is_online` column;
  `UpdateLastSeen` also marks a device online.
- Cycle 4: `LifecycleIngester` — `SQSConsumer[Lifecycle]` handler.
- Cycle 5: `PresenceSweeper` goroutine — 30s ticker → `Sweep` → persist
  + `audit.presence` log line.
- Cycle 6: end-to-end lifecycle ingest (moto SQS + Postgres).
- Cycle 7: `cmd/cp-ingest` runs both consumers + the sweeper over one
  shared `Presence`.
- Cycle 8: Terraform — the `cp-presence-lifecycle` queue reuses
  `modules/sqs-ingest`; `cp-ingest-service` gains the lifecycle env vars.
- Cycle 9: sweeper integration test against Postgres (added beyond the
  9-cycle plan to close AC 1 end to end, not just at the unit seam).
- Cycle 10: docs — `architecture.md` + `CONTEXT.md`.

**Model change.** #07 derived `is_online` at read time from `last_seen`;
#08 makes it a stored column maintained by three writers — heartbeat
(→online), sweeper (stale→offline), lifecycle (both edges). The
disconnect fast-path needs stored state: a `disconnected` event must
show offline even while `last_seen` is still fresh, which a pure
`last_seen` derivation cannot express. This is within the settled
presence design (the PRD anticipated `PresenceSweeper` /
`LifecycleIngester`), so no ADR — the issue spec'd it ("update
`devices.last_seen_state` or equivalent").

**Documentation criterion.** Discharged — `architecture.md` (Ingest
workers, module table, diagrams, storage, cloud-infra status) and
`CONTEXT.md` (the Presence glossary entry) updated in cycle 10.

**One acceptance criterion deferred.** "Pulling power on the dev Mac …
offline in the API within 60s" is a Wave-0 manual hardware verification
— no hardware or deployed CP here. It belongs to the Wave-0 bench smoke
(#12); the disconnect fast-path it exercises is covered automatically by
the cycle-6 end-to-end test (5s) and the sweeper backstop by cycle 9.

**Known limitation.** `cp-ingest`'s in-memory `Presence` is not
rehydrated from Postgres on restart; heartbeats refill it within ~30s.
A device that dies in the narrow window during a `cp-ingest` restart,
before its first post-restart heartbeat, would not be swept until it
next appears. Out of #08 scope — flag for a future hardening pass.
