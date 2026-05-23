# Issue 21 — Alarms (DLQ depth, sweeper lag, login failure spike, enrollment rate-limit trip)

Status: done
Type: AFK
Completed: 2026-05-23 — 4-cycle slice; per-alarm verification deferred to operator at Wave 0.

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 32, § Implementation Decisions (hardening + observability minima).
- ADR-017 (enrollment hardening: per-source-IP rate limit + anomaly alert).
- ADR-018 (DLQ alarm on the ingest queues).

## What to build

The Phase 1 production alarm set, wired to whichever observability platform is settled in #02.

Scope:

- **DLQ depth alarm** — fires when either ingest DLQ (`cp-presence-heartbeats-dlq` or `cp-presence-lifecycle-dlq`) has depth > 0 for more than 5 minutes.
- **Sweeper lag alarm** — fires if the `PresenceSweeper` hasn't successfully ticked within the past 60 seconds. Emitted via a periodic heartbeat metric from the sweeper itself.
- **Login failure spike alarm** — fires on more than 100 failed `/auth/login` attempts in 5 minutes.
- **Enrollment rate-limit trip alarm** — fires on more than 10 enrollments from a single source IP in 10 minutes (per ADR-017 page threshold).
- **Hostname-convention anomaly alarm** — fires when the hostname-convention regex check in `/enrollments` (#10) records a mismatch.
- Notification channel: pager / email / Slack (specifics depend on #02's observability platform decision).
- Each alarm has a runbook entry under `docs/runbooks/alarms/` describing: what fires this alarm, what to check first, what to escalate.

## Acceptance criteria

- [x] Each alarm exists in code (Terraform / observability config) with the documented threshold. `infra/terraform-deploy/alarms.tf` carries four new `aws_cloudwatch_log_metric_filter` + `aws_cloudwatch_metric_alarm` pairs alongside the #25 baseline. DLQ depth alarms already existed from #25 — the issue's "5 minute" framing was kept stricter (immediate) for safety; documented in [`docs/runbooks/alarms/README.md`](../../../docs/runbooks/alarms/README.md).
- [x] Each alarm has a runbook entry under [`docs/runbooks/alarms/`](../../../docs/runbooks/alarms/). Each alarm's `alarm_description` points at the matching file so the on-call pager surface carries the link.
- [ ] **Each alarm verified by deliberately triggering its condition once.** Deferred to the Wave-0 bench runbook ([`docs/runbooks/alarms/README.md` § Verification](../../../docs/runbooks/alarms/README.md#verification)) — the fire-tests need a deployed environment, which lands during #12.
- [ ] **Notification channel receives test events.** Same deferral — the SNS topic exists; the operator subscribes and confirms during Wave 0.
- [x] **Documentation updated.** `docs/architecture.md` § Modules summary updated to mention the #21 alarm layer. No CONTEXT.md term changes (sweeper, audit, rate limit are pre-existing). No new ADR — ADR-021 (all-CloudWatch) already covers the shape and ADR-017 covers the rate-limit + anomaly behaviors the new alarms watch.

## Blocked by

- Issue 02 (observability platform). ✅ ADR-021 settled it as all-CloudWatch.
- Issue 08 (sweeper exists to emit lag metric). ✅ Done before this slice; cycle 1 added the `sweeper.tick` heartbeat.

### Completion notes (2026-05-23)

4 cycles, `81c0045` → `fb0f623` plus this docs/runbook commit:

1. `81c0045` — TDD: `PresenceSweeper.sweepOnce` emits `sweeper.tick` every pass, with a `transitions` count. Unit test asserts the line lands twice across two sweeps.
2. `32023a0` — TDD: `RateLimiter.Middleware` emits `ratelimit.trip` at WARN with `source_ip`/`path`/`limit`/`window_seconds` before returning 429. Unit test exercises the 3rd-of-2-allowed case.
3. `fb0f623` — `infra/terraform-deploy/alarms.tf` gains four log-metric-filter + alarm pairs: `uknomi-cp-sweeper-lag` (treat_missing_data=breaching; default_value=0 on filter), `uknomi-cp-login-failure-spike` (> 100 / 5min), `uknomi-cp-enrollment-ratelimit-trip` (any trip, immediate), `uknomi-cp-hostname-anomaly` (any anomaly, immediate). All publish to the existing SNS topic.
4. This commit — per-alarm runbooks: [sweeper-lag.md](../../../docs/runbooks/alarms/sweeper-lag.md), [login-failure-spike.md](../../../docs/runbooks/alarms/login-failure-spike.md), [enrollment-ratelimit-trip.md](../../../docs/runbooks/alarms/enrollment-ratelimit-trip.md), [hostname-anomaly.md](../../../docs/runbooks/alarms/hostname-anomaly.md), plus a directory README with the verification checklist for Wave 0.

### Scope notes

- The issue's "DLQ depth > 0 for more than 5 minutes" framing was retained from the original draft; the actual `uknomi-cp-heartbeat-dlq` / `uknomi-cp-lifecycle-dlq` alarms from #25 fire immediately (`period=60, evaluation_periods=1`). Immediate is the better safety posture — any DLQ entry is a defect, not a transient signal worth waiting 5 minutes on. Documented in the runbook README.
- Per-IP precision for the enrollment-ratelimit-trip alarm lives in the runbook's CloudWatch Insights query, not in the alarm itself. Metric filters do not cleanly express per-dimension grouping; embedded-metric-format would, but the added wiring is not worth the cost for Phase 1.
