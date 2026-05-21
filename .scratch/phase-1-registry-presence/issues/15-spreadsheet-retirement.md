# Issue 15 — Spreadsheet retirement (Mac portion)

Status: ready-for-human
Type: HITL

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 35–36 (two-gate exit criterion: ship gate + retirement gate).
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1.

## What to build

The operational closure of Phase 1 for the Mac portion: the spreadsheet's Mac rows are removed, the file is archived, and the dashboard becomes the only Mac source-of-truth. Linux rows remain in the spreadsheet with a banner until Wave 3 lands.

Scope:

- Pick the retirement date (target: ~2 weeks after Wave 2's ship gate; team picks the calendar date).
- On the retirement date: remove all Mac rows from `uknomi-macmini-devices.xlsx`. Add a top-banner note: "Mac inventory is in the CP dashboard at <URL>; this spreadsheet retains Linux devices only until Wave 3 completes."
- Move the prior version of the spreadsheet to an archive folder for reference.
- Communicate the change to operators (Slack, email, however the team typically does this).

## Acceptance criteria

- [ ] Retirement date picked and communicated at least 7 days in advance.
- [ ] Mac rows removed from the spreadsheet on the chosen date.
- [ ] Banner added pointing at the dashboard URL.
- [ ] Prior spreadsheet version archived.
- [ ] Operators notified.

## Blocked by

- Issue 14 (Wave 2).
