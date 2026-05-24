# Phase 1 followups

Open items left from Phase 1 (Wave 0 enrollment + auto-deploy slice). None are session-blocking; they were parked when the team pivoted to Phase 2. Pick by priority when needed.

This folder is an **ephemeral working tray** (per [`docs/agents/issue-tracker.md`](../../docs/agents/issue-tracker.md)) — promote anything durable to an ADR or the architecture doc; discard the rest when it stops mattering.

## Actionable issues

| #  | File                                                                     | Type | Estimate |
|----|--------------------------------------------------------------------------|------|----------|
| 01 | [Postgres `devices` orphan GC tool](./issues/01-orphan-gc-tool.md)       | AFK  | 2–3 hr   |
| 02 | [Empty deploy-root log group cleanup](./issues/02-empty-log-group.md)    | AFK  | 15 min   |
| 03 | [§5b smoke: network-drop + power-yank on next bench device](./issues/03-bench-smoke-physical.md) | HITL | 30 min on hardware |
| 04 | [DB connectivity from operator laptop — design decision](./issues/04-db-connectivity-decision.md) | HITL | 30 min decision, then implementation |

## Watchlist (not yet actionable)

| Item | Note |
|---|---|
| `audit-mirror-stale` alarm cleanup | Tonight's 00:05 UTC scheduled run should publish `AuditMirrorCompletions` and the alarm should transition OK within ~2 hours. Verify in the morning. If still firing 24h after 2026-05-25 00:05 UTC, investigate. |
| `audit-mirror` first-run logging silently lost | The 2026-05-24T00:05:30 UTC scheduled run produced exit code 0 but zero log lines (empty stream). Today's image works (verified by manual `run-task`). Watching tonight's scheduled run confirms it stays healthy; if the empty-log pattern recurs, dig into `awslogs` driver buffer-flush behaviour for short-lived tasks. |
| Auto-redirect on uninitialized system | Covered by unit test with mocked API. Not verified live (the production system is initialized; can't reproduce without a fresh deploy). Verify when a staging environment exists (ADR-020 long-term target). |
| `web/package-lock.json` portability gotcha | npm 11 darwin-arm64 vs npm 10 linux-x64 (CI's `node:22-alpine`). Always regenerate lockfiles inside the container after bumping deps; do not `npm install` locally without verifying `npm ci` still works in the CI image. |

## What landed in the session that produced this folder

The full activity log lives in the handoff at `/var/folders/vl/fgtgcbg56553xp0_ds12m8t00000gn/T/handoff-XXXXXX.md.vHeMI5dOLI`. Headline: Wave 0 enrollment landed (one field Mac live), auth-UX shipped (token persistence + protected routes + server-side refresh revocation), lifecycle fast-path hardened (correlation-id stamping + IAM AttachPolicy fix), ProvisionDevice rollback on failure, and the CI/CD auto-deploy slice from ADR-027 (the follow-on that this folder originally documented as #6).
