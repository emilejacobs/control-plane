# Issue 13 — Wave 1 (Pilot site) rollout

Status: ready-for-human
Type: HITL

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 27.
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — Wave 1.

## What to build

The first real-site rollout: a single client site, ~3–5 devices, with one operator watching the dashboard for one week. Exists to surface anything Wave 0 (a bench device under our control) couldn't.

Scope:

- **Stakeholder selection.** Pick the pilot client — needs the team's product owner to choose. Tell the client the rollout is happening, explain the read-only nature, set expectations on what they should and shouldn't expect.
- Coordinate with the client to ensure their Macs are available for Mosyle dispatch within the window.
- Issue bootstrap install via Mosyle for the pilot devices.
- Watch the dashboard for one full week. Daily check: every device shows online with current `last_seen`; presence transitions match real-world device state (power cycles at the site); no DLQ depth; no alarm spikes.
- Document any defects in new issues. Decide for each: blocks Wave 2 (must fix), or backlog (fix in parallel with Wave 2).

## Acceptance criteria

- [ ] Pilot client selected and informed.
- [ ] All pilot devices enrolled, online, and presence accurate within 60s for one full week.
- [ ] Any defects surfaced are filed with appropriate priority; blockers resolved before Wave 2 starts.
- [ ] Wave-1 retrospective recorded (what surprised us, what to change for Wave 2).

## Blocked by

- Issue 12 (Wave 0).
