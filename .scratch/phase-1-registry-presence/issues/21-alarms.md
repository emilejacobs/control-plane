# Issue 21 — Alarms (DLQ depth, sweeper lag, login failure spike, enrollment rate-limit trip)

Status: ready-for-agent
Type: AFK

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

- [ ] Each alarm exists in code (Terraform / observability config) with the documented threshold.
- [ ] Each alarm has a runbook entry.
- [ ] Each alarm has been verified by deliberately triggering its condition once (e.g., posting a message directly to the DLQ).
- [ ] Notification channel receives test events on alarm fire.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 02 (observability platform).
- Issue 08 (sweeper exists to emit lag metric).
