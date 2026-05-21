# Issue 12 — Wave 0 (Bench) end-to-end smoke

Status: ready-for-human
Type: HITL

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 26, 30, 35–36.
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — Wave 0 row.

## What to build

The first real end-to-end exercise of the system on hardware uKnomi controls. The Phase 0 dev device (`dev-mac-mini-emile`) is decommissioned, the same Mac is re-provisioned through the new install module against Terraform-managed IoT Core resources, and the full stack is verified: agent connects, heartbeat ingests, dashboard shows the device online, presence transitions work, login + TOTP works.

This slice is HITL — it's a manual rollout step with checklist execution, not an AFK code change.

Scope:

- Decommission Phase 0 device: revoke the manual cert (`aws iot update-certificate --new-status REVOKED`), detach + delete the cert, delete the thing, uninstall the agent (`launchctl unload` + delete LaunchDaemon plist + remove binary). Document in a one-time `runbooks/phase-1-wave-0-bench.md`.
- Re-provision via Terraform: `terraform apply` produces a fresh thing + cert under the new Wave 0 name (e.g., `bench-mac-mini-01`).
- Run the new install module on the same Mac, against the production CP (or a Wave-0 dedicated environment if CI/CD shape from #02 says so).
- Verify the full ship-gate checklist on this one device:
  - [ ] Device appears in `GET /devices`.
  - [ ] Presence transitions correctly when the agent restarts / network drops / power yanks.
  - [ ] Login + TOTP works against the deployed dashboard.
  - [ ] Cert expiry surfaces correctly on the per-device view.
  - [ ] Audit log captures the enrollment and a manual restart.
  - [ ] No DLQ messages, no alarm fires during the smoke window (~30 min).

## Acceptance criteria

- [ ] All Wave-0 checklist items above pass.
- [ ] The `runbooks/phase-1-wave-0-bench.md` runbook is written and accurate (a second engineer could repeat it).
- [ ] Any defects found are filed as new issues; this slice does not "fix-as-you-go" — it surfaces gaps for explicit handling.
- [ ] The bench Mac remains the demo device for future grilling sessions and bench tests; it is **not** a permanent fleet member (per the 2026-05-21 grilling decision).

## Blocked by

- Issue 11 (install module).
- Issue 17 (fleet view).
- Issue 18 (per-device view).
- Issue 19 (structured logs + correlation IDs library — needed to verify auditability).
