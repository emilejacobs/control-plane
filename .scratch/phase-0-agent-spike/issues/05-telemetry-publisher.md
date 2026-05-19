# Issue 05 — Telemetry heartbeat publisher

Status: ready-for-agent

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

The `telemetry-publisher` module, publishing a periodic heartbeat on `devices/{id}/telemetry` independent of any in-flight command. Provides device-liveness observability that doesn't depend on operator-initiated commands.

Scope:

- `telemetry-publisher` module: takes `{interval time.Duration, collectors []func() map[string]any, transport Transport, deviceID string}`. On each tick, runs all collectors, merges their results into a single JSON payload, publishes to `devices/{id}/telemetry`.
- Default interval: 30 seconds (configurable via the agent's config file).
- Default collectors at Phase 0: device id, agent version, OS, uptime, last-command timestamp (or null if none yet).
- Collector error handling: a panicking or erroring collector is logged (structured JSON per ADR-011) and skipped; the publisher continues. A failing collector must not crash the next tick.
- Each published payload carries a fresh `correlation_id` (a heartbeat is its own correlation, not tied to any inbound command).

Tests (smoke-only per the cut in the PRD's Testing Decisions):

- One test asserting an interval tick produces a publish (with a fast interval and a fake transport).
- One test asserting a collector returning an error is logged and the next tick still fires (resilience).

## Acceptance criteria

- [ ] Telemetry heartbeats appear on `devices/{id}/telemetry` at the configured interval when the agent is running.
- [ ] A subscriber (e.g. `mosquitto_sub` or `agent-cli` extended with a telemetry-watch subcommand — implementer's choice) sees periodic publishes.
- [ ] Each payload includes device_id, agent version, OS, uptime, last-command timestamp (nullable), and a `correlation_id`.
- [ ] Deliberately panicking one collector does not stop subsequent ticks from publishing the other collectors' values.

## Blocked by

- [Issue 01 — First light](./01-first-light.md)
