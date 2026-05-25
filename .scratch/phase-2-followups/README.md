# Phase 2 followups

Open items left from Phase 2's first two slices ([service-status](../phase-2-service-status/) + [allow-list overrides](../phase-2-allow-list-overrides/)). None are session-blocking; they were either surfaced live, deferred-by-design, or known limitations to revisit once their unblocker lands.

Same posture as [`phase-1-followups/`](../phase-1-followups/): **ephemeral working tray** per [`docs/agents/issue-tracker.md`](../../docs/agents/issue-tracker.md). Promote anything durable to an ADR or the architecture doc; discard the rest when it stops mattering. **Mark items `done` inline (don't delete the row) so the record of "we did this" survives the next session.**

When closing an item, add a one-line "done YYYY-MM-DD (commit `xxx`)" stamp to the table cell so a future agent can `grep` for the resolution.

## Actionable issues

| #  | File                                                                          | Type | Estimate | Source slice |
|----|-------------------------------------------------------------------------------|------|----------|--------------|
| 01 | [`device_services` sweeper for stale rows](./issues/01-device-services-sweeper.md) | AFK  | 2–4 hr   | slice 1 (more visible after slice 2) |
| 02 | [CloudWatch alarm for `cmd-result` DLQ](./issues/02-cmd-result-dlq-alarm.md)  | AFK  | 30 min   | slice 2 |
| 03 | [Path-filter for `internal/dispatcher/**`](./issues/03-dispatcher-path-filter.md) | AFK  | 5 min    | slice 2 |
| 04 | [Repo-root cleanup: `cp-ingest` binary + `slice2.tfplan`](./issues/04-repo-root-cleanup.md) | AFK | 5 min | slice 2 |

## Future Phase 2 slices (each gets its own PRD when picked up)

- **Per-device log-path override** — same pattern as slice 2's allow-list override but for `log_allow_list`. Defer until at least one device needs a non-default log path.
- **Bulk allow-list / log-allow-list edit by site** — "apply this list to every Mac at site X". Trivial loop on top of the existing per-device PUTs. Defer until the operator actually wants it.
- **Edge UI proxy** — biggest Phase 2 slice still pending; the roadmap flags it as the highest-risk slice (iframe-proxy may break Edge UI features). Phase 4 fallback if proxying is too lossy.
- **Camera snapshot fetch** — small slice on top of the Edge UI proxy.

## Watchlist (not yet actionable)

| Item | Note |
|---|---|
| Agent install-time-frozen `version` field | Slice 1 followup #1; carried forward into slice 2 (bench Mac startup log still reports `version: 2b6cfd0` despite running `099dd7f`). Cosmetic. Real fix waits on Phase 3's agent self-update primitive landing a build-time `-ldflags -X main.version=…` stamp. |
| `mac-mini-rollout/bin/` uncommitted | Slice 1 + slice 2 both staged new binaries here (`099dd7f` is the current). Convention seems to be: binaries staged locally for hot-swap, not committed. Codify the convention or commit explicitly if it starts to matter. |
| Phase 3 absorbs five unsigned cmd handlers | When Phase 3's signed-command pipeline (per ADR-013) lands, it wraps `heartbeat`, `service.status`, `service.restart`, `config.update`, **and** the upcoming `log.tail` in the signed envelope. ADR-028's "two unsigned handlers turn into four" line will need an amend to "...five" when log-tail ships. Track in the log-tail issue rather than here. |
| Live verification of slice 2 in continuous use | Slice 2 went live 2026-05-24 and smoke-tested end-to-end on the bench Mac. Watch for DLQ growth on `uknomi-cp-cmd-result` over the next week — issue 02 (above) addresses the missing alarm, but until then, eyeball it. |
| sister-repo (mac-mini-rollout) one-commit-ahead | The single local commit `35e712a` from the slice-1 session (module 11 writes `service_allow_list` + `service_status_interval`) is still local-only. No remote configured for that repo. |

## What landed in the session that produced this folder

Slice 2 (per-device allow-list overrides). 19 commits on `main` (`713b75d` → `4e62d67`), one targeted `terraform apply` against deploy-root, bench-Mac agent hot-swapped to `099dd7f`. The full punch list is in [the issue 01 completion comment](../phase-2-allow-list-overrides/issues/01-end-to-end.md#comments). One bug surfaced live (CORS preflight didn't advertise PUT — fixed in `4e62d67`); rest of the slice landed clean.
