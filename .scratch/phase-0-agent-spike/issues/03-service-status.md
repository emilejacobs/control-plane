# Issue 03 — service.status command (Mac + Linux impls)

Status: ready-for-agent

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

A new command, `service.status`, that returns the running state of a named OS service on both macOS (via `launchctl`) and Linux (via `systemctl`). Establishes the `service-backend` module and proves the build-tag separation pattern works for OS-specific implementations behind a common interface.

Scope:

- `service-backend` module: a Go interface with `Status(name string) (State, error)`. `State` is a small enum: at minimum `Running`, `Stopped`, `Unknown`. The interface lives in a single file shared across OSes; the implementations are build-tag separated.
- macOS implementation behind `//go:build darwin`, shelling out to `launchctl print` or `launchctl list` (pick the cleaner output to parse).
- Linux implementation behind `//go:build linux`, using `systemctl is-active` (exit-code driven) or `systemctl status` (whichever gives the cleanest state mapping).
- A fake `service-backend` implementation used by dispatcher tests so handler tests do not shell out.
- The `service.status` command handler registered with the dispatcher (following the registry pattern established in Issue 01).
- The command works through the existing `agent-cli`: `agent-cli service.status <device-id> <service-name>`.

Handler tests (per ADR-012):

- Unit tests via the fake `service-backend` covering: known service returns `Running`/`Stopped`, unknown service returns failure envelope with stable error code (e.g. `service.not_found`), `correlation_id` propagation as established in Issue 01.

## Acceptance criteria

- [ ] `service.status` is registered with the dispatcher and responds with a structured result containing the resolved state.
- [ ] Calling `service.status` against the dev-laptop agent returns a state for a known launchd job (on Mac) or systemd unit (on Linux).
- [ ] Querying an unknown service name returns a failure envelope with a stable error code, not a panic or generic 500.
- [ ] Unit tests for the handler use the fake `service-backend`; the dispatcher tests from Issue 01 still pass unchanged.
- [ ] The cross-platform build matrix (Issue 02) is green — both `darwin/*` and `linux/arm64` compile cleanly.
- [ ] `correlation_id` is propagated end-to-end as established in Issue 01.

## Blocked by

- [Issue 01 — First light](./01-first-light.md)
