# Issue 04 — service.restart command (Mac + Linux impls)

Status: ready-for-agent

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

A new command, `service.restart`, that restarts a named OS service and reports success or failure with the underlying tool's output. Critical that failures from the OS tool surface as failure envelopes — operators must not be told a restart succeeded when it did not (User Story 23).

Scope:

- Extend the `service-backend` interface with `Restart(name string) error` (or `(Result, error)` if capturing stdout/stderr cleanly requires it — implementer's call).
- macOS implementation shells out to `launchctl kickstart -k system/<name>` (or equivalent), capturing exit code, stdout, and stderr.
- Linux implementation shells out to `systemctl restart <name>`, capturing exit code, stdout, and stderr.
- The `service.restart` command handler registered with the dispatcher.
- Failure path: when the underlying tool exits non-zero, the handler returns a failure envelope including the captured stderr/exit code so operators see what actually went wrong.
- Result envelope includes `started_at` and `finished_at` timestamps per the schema established in Issue 01.

Handler tests (per ADR-012):

- Unit tests covering both success and failure paths via the fake `service-backend`.
- Test that a fake returning a non-zero-exit-style error produces a failure envelope with the captured output, not a success envelope.

## Acceptance criteria

- [ ] `service.restart` is registered with the dispatcher and works on both macOS and Linux against the dev-laptop agent.
- [ ] Restart of a chosen low-stakes test service returns success and shows the elapsed `started_at` → `finished_at` timing.
- [ ] Restart of a non-existent or non-restartable service returns a failure envelope with the captured tool error, not a generic 500.
- [ ] Unit tests cover both success and failure paths via the fake `service-backend`.
- [ ] Cross-platform CI matrix (Issue 02) is green.

## Blocked by

- [Issue 03 — service.status](./03-service-status.md)
