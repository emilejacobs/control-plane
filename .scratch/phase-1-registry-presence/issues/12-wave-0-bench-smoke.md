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

- [ ] All Wave-0 checklist items above pass. *(HITL — verified on the bench Mac.)*
- [x] The `runbooks/phase-1-wave-0-bench.md` runbook is written and accurate (a second engineer could repeat it). *(Landed at `docs/runbooks/phase-1-wave-0-bench.md`.)*
- [ ] Any defects found are filed as new issues; this slice does not "fix-as-you-go" — it surfaces gaps for explicit handling. *(HITL — fires when the smoke runs.)*
- [ ] The bench Mac remains the demo device for future grilling sessions and bench tests; it is **not** a permanent fleet member (per the 2026-05-21 grilling decision). *(Stated in the runbook's "What this runbook is *not*" section; confirmed on smoke day.)*

## Blocked by

- Issue 11 (install module).
- Issue 17 (fleet view).
- Issue 18 (per-device view).
- Issue 19 (structured logs + correlation IDs library — needed to verify auditability).
- Issue 01 (Phase 1 Terraform — VPC, ALB, RDS, Fargate, CP deployment). *Realistically blocking even though the tracker did not list it; the runbook calls this out as the first prerequisite to verify.*

## Comments

### 2026-05-22 — runbook landed; HITL execution still pending

#12's only AFK-shaped slice is AC2 — write the runbook. That landed at
`docs/runbooks/phase-1-wave-0-bench.md`. The runbook walks a second
engineer through: decommissioning the Phase 0 device (Terraform-driven
where state still exists; a manual `aws iot` fallback otherwise),
applying Phase 1 Terraform, populating the real bootstrap key in Secrets
Manager, baking the install package, running `setup.sh --phase 1` on the
bench Mac, the six § 5 smoke checks, the 30-minute monitoring window,
and the rollback procedure.

**Findings surfaced while writing it:**

1. *#01 is a hidden prerequisite.* #12's "Blocked by" listed #11/#17/#18/#19
   but not #01. Wave 0 requires a deployed CP (ALB + Fargate + RDS), which
   only lands with #01. Added #01 to "Blocked by" and made it the first
   line of the runbook's prerequisites table.
2. *#10's CI baking is the live gap.* #10's AC2 (the mac-mini-rollout CI
   workflow that bakes `secrets/cp-bootstrap-key`) was deferred to that
   repo. The runbook documents a manual bake-by-hand step the engineer can
   run on the build machine until the CI lands.
3. *Real bootstrap key is not yet set.* `terraform apply` seeds a
   placeholder; the operator runs `aws secretsmanager put-secret-value`
   once. The runbook spells this out.
4. *Hostname-convention reconciliation.* The mac-mini-rollout repo's
   default device names (`macmini-staging-*`, `STORE-CHAIN-LOCATION-macmini`)
   do not match the CP's `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$` regex —
   every enrolment would log the (non-blocking) `audit.enrollment.anomaly`
   alert. The runbook sidesteps it for the bench by setting the hostname
   to `mac-mini-bench-01` before `setup.sh` runs; the broader naming
   reconciliation is #24.

**Status remains `ready-for-human`.** ACs 1, 3, and 4 are end-to-end
checks performed by a human at the bench Mac with the prerequisites met;
they cannot be discharged from this turn. AC2 is the only AFK slice and
is checked.

**Documentation criterion.** Discharged — `docs/runbooks/phase-1-wave-0-bench.md`
is the documentation deliverable. `architecture.md` and `CONTEXT.md`
unchanged (no new components or domain terms); no ADR (the runbook
records procedure, not a hard-to-reverse decision).
