# Issue 08 — Sweeper + IoT lifecycle fast-path

Status: ready-for-agent
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

- [ ] A device with `last_seen` set 91s in the past is marked offline by the sweeper within 30s.
- [ ] A simulated IoT `disconnected` event flips the device to offline within 5s without waiting for the sweeper.
- [ ] Successive sweeper ticks do not emit duplicate transitions for already-offline devices.
- [ ] A reconnecting device with a fresh heartbeat is marked online and an online transition is emitted.
- [ ] Pulling power on the dev Mac (Wave-0 manual verification) results in the device showing offline in the API within 60s.
- [ ] All seven `Presence` behaviors from the PRD's testing decisions section are covered by unit tests with a fake clock.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 07.
