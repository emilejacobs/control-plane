# Issue 08 — Field deployment: Linux device

Status: ready-for-human

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

The HITL acceptance test for Phase 0 on Linux: deploy the agent to one Linux device (Pi or Radxa, lab or quiet client site) and validate that the same binary behaviour holds on Linux. Lower stakes than Issue 07 because the Linux fleet is deprecating (per ADR-007), but still required — the value of this issue is proving the **identical** compiled binary works on both OSes (User Story 12).

Scope (HITL):

- **Pick a device.** Lab Pi or Radxa is acceptable since the Linux fleet is deprecating per ADR-007; a quiet client site is also fine. Bar is lower than the Mac deployment.
- Provision an IoT Core thing + mTLS cert via the runbook from Issue 01.
- Install the `linux/arm64` binary (from Issue 02 artefacts) + cert + systemd unit (Issue 06).
- Run the same success-criteria validation as Issue 07:
  - `heartbeat` round-trip <2s, 10 trials, median recorded.
  - `service.status` returns expected state for a chosen test service.
  - `service.restart` succeeds.
  - Network-blip reconnect test.
  - Telemetry heartbeats observed for ≥10 minutes.
- The restart test target on Linux **can be a simple owned no-op systemd unit created specifically for the test** — the existing Linux fleet services (Zabbix/Raven/Tailscale) are not great restart targets and Pi deprecation makes them low-value anyway.
- Capture results in `.scratch/phase-0-agent-spike/results-linux.md`.

The primary value of this issue is confirming that the **identical compiled binary behaviour** holds on Linux — same envelope schema, same command set, same logging — proving the build-tag separation works as intended (per ADR-002).

## Acceptance criteria

- [ ] Linux device selected and prepared.
- [ ] IoT Core thing provisioned and cert installed.
- [ ] Agent installed via systemd unit and confirmed starting on boot.
- [ ] 10 trials of `heartbeat` round-trip recorded; median <2 seconds.
- [ ] `service.status` returns the expected state for the chosen test service.
- [ ] `service.restart` of the test service succeeds.
- [ ] Network-blip reconnect test passes.
- [ ] Telemetry heartbeats observed for at least 10 minutes.
- [ ] Results captured in `.scratch/phase-0-agent-spike/results-linux.md`.
- [ ] The same binary artefact used on the Mac in Issue 07 is **not** reused here (Linux uses `linux/arm64`); but the source commit is the same, and the result file notes the commit SHA for traceability.

## Blocked by

- [Issue 02 — Cross-compile + CI](./02-cross-compile-ci.md)
- [Issue 04 — service.restart](./04-service-restart.md)
- [Issue 05 — Telemetry publisher](./05-telemetry-publisher.md)
- [Issue 06 — Service unit files](./06-service-unit-files.md)
