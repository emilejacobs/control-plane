# Issue 14 — Wave 2 (Mac fleet) bulk rollout

Status: ready-for-human
Type: HITL

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 28.
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — Wave 2.

## What to build

The bulk rollout to the remaining ~20 Mac Minis across all other client sites. Wave 2 is the ship-gate milestone — once all Macs are online, presence accurate, the ship gate is satisfied.

Scope:

- Mosyle dispatches the install package to all remaining Mac sites in batches (suggested: by client, rather than all-at-once, so any single-site issue surfaces in a contained blast radius).
- Engineer watches dashboard during each batch; checks for spikes in DLQ, alarm fires, enrollment rate-limit trips.
- Reconcile dashboard against the spreadsheet at the end: every Mac row from the spreadsheet is now a `enrolled` device row in the registry.

## Acceptance criteria

- [ ] All ~20 Wave-2 Macs enrolled, online, and presence accurate.
- [ ] Total Mac count in registry (Wave 0 + Wave 1 + Wave 2) reconciles with the Mac portion of the spreadsheet.
- [ ] No DLQ messages outstanding at end of rollout.
- [ ] Ship gate declared satisfied (per PRD § User Story 35): all ~25 Macs enrolled, presence accurate within 60s, dashboard groups by client/site, login + TOTP works.

## Blocked by

- Issue 13 (Wave 1).
