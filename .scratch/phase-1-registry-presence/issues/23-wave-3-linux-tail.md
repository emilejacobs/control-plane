# Issue 23 — Wave 3 (Linux tail) rollout — parallel track, does NOT gate Phase 1 exit

Status: ready-for-human
Type: HITL

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 29, § Out of Scope (Linux rollout deferred from Phase 1 exit gate).
- ADR: ADR-007 (Pi/Radxa minimal agent; deprecating tier).
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — Linux deferral note.

## What to build

The rollout of the Linux install script (#22) to the 36 Pis and 2 Radxas in the fleet. **This issue is explicitly outside Phase 1's exit gate** — it runs as a parallel track that does not block ship-gate or retirement-gate declarations.

Scope:

- Plan and schedule the rollout. No Mosyle for Linux — each device is enrolled either by an operator on-site running the install script, or by SSH-ing in remotely and running it. Specifics depend on per-site access.
- Track progress against the Linux portion of the spreadsheet; mark each device as enrolled or "not feasible — replace with Mac" as it completes.
- Retire Linux rows from the spreadsheet as they get enrolled (or when devices are replaced with Macs per the consolidation direction).
- When all Linux devices are either enrolled or explicitly retired, the spreadsheet can be fully archived.

## Acceptance criteria

- [ ] All 38 Linux devices are either enrolled in CP or explicitly marked decommissioned/replaced.
- [ ] The Linux portion of the spreadsheet is reconciled with the registry.
- [ ] Once both Mac (#15) and Linux portions are retired, the spreadsheet is fully archived.

## Blocked by

- Issue 22 (Linux install script exists).
