# Issue 07 — Field deployment: Mac at a real client site

Status: done (personal-Mac spike — see [`../results-mac.md`](../results-mac.md) for what was and wasn't covered vs the original acceptance criteria)

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

The HITL acceptance test for Phase 0 on macOS: deploy the agent to one real Mac Mini at one real client site and validate that the Phase 0 success criteria hold under actual network conditions. This is the moment the architecture meets reality — see the PRD's "Primary risk" section.

Scope (HITL — requires human coordination, not autonomous):

- **Pick a client site** for the deployment. Selection criteria: low-stakes (a service restart on the chosen target service will not disrupt critical operations), reachable for a phone-call validation cycle, representative of the typical client-site NAT and firewall posture.
- **Schedule a deployment window** with the operator(s) for that site.
- **Pick a target service for the restart test** — one whose restart is genuinely safe (idempotent, fast, no downstream effects). Confirm with the operator.
- Provision an IoT Core thing + mTLS cert for the device using the runbook from Issue 01.
- Install the cross-compiled `darwin/arm64` binary (from Issue 02 artefacts) + cert + LaunchDaemon (Issue 06).
- Run the Phase 0 success-criteria validation:
  - `heartbeat` round-trip completes in under 2 seconds — record actual times across 10 trials.
  - `service.status` returns the expected state for the target service.
  - `service.restart` of the target service succeeds and the service is healthy afterwards.
  - Deliberate network blip (disable Wi-Fi for ~60s, re-enable) — agent reconnects automatically and the next `heartbeat` succeeds.
  - Telemetry heartbeats are visible on the telemetry topic at the configured interval (observed for ≥10 minutes).
- Capture results in `.scratch/phase-0-agent-spike/results-mac.md`: timing measurements, exact services tested, any anomalies, screenshots if useful.

**If any criterion fails:** capture exactly what happened and do not retry blindly. A failed Phase 0 on Mac is a meaningful project-level signal (per the PRD's primary risk — client-site firewalls blocking MQTT-over-WSS), not a bug to grind through. Flag for human review immediately.

## Acceptance criteria

- [ ] Client site selected and operator approval recorded in the results file.
- [ ] Target service for restart test selected and approved.
- [ ] IoT Core thing provisioned and cert installed on the device.
- [ ] Agent installed via LaunchDaemon and confirmed starting on boot.
- [ ] 10 trials of `heartbeat` round-trip recorded; median <2 seconds.
- [ ] `service.status` of the target service returns the expected state.
- [ ] `service.restart` of the target service succeeds; service is healthy afterwards.
- [ ] Network-blip reconnect test passes.
- [ ] Telemetry heartbeats observed for at least 10 minutes.
- [ ] Results captured in `.scratch/phase-0-agent-spike/results-mac.md`.

## Blocked by

- [Issue 02 — Cross-compile + CI](./02-cross-compile-ci.md)
- [Issue 04 — service.restart](./04-service-restart.md)
- [Issue 05 — Telemetry publisher](./05-telemetry-publisher.md)
- [Issue 06 — Service unit files](./06-service-unit-files.md)
