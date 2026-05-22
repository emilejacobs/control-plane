# Issue 20 — Audit log surface

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 5, 31, § Implementation Decisions (schema for `audit_log`).
- Architecture: `docs/architecture.md` § Security — "Append-only audit log in Postgres + daily S3 mirror."

## What to build

The audit log: a Postgres table written to by every state-changing endpoint and by the ingest worker for security-relevant events (DLQ entries, anomaly alerts, enrollment outcomes), plus a daily S3 mirror for tamper-evidence and long-term retention.

Scope:

- Schema: `audit_log` table per PRD sketch (`id`, `at`, `actor_id`, `actor_type`, `action`, `resource_kind`, `resource_id`, `correlation_id`, `source_ip`, `user_agent`, `payload jsonb`, `outcome`). Migration adds it.
- Write API: a small `audit.Write(ctx, AuditEntry)` helper in the same logging library from #19; uses the same context-bound `correlation_id`. Append-only — no update/delete API exists.
- Middleware that automatically writes an audit entry for every state-mutating request (`POST`, `PUT`, `PATCH`, `DELETE`) with the request method, path, response status, and the operator/agent identity from context. Plus explicit calls in:
  - Enrollment handler (success, rate-limit trip, hostname-convention alert).
  - Auth handlers (first-run claim, login success/failure, refresh, TOTP enrollment, recovery code use).
  - Ingest worker DLQ writes.
- S3 mirror: a daily job (Fargate task on a schedule, or a goroutine in `cp-ingest`) exports the prior day's audit_log rows as a gzipped JSON Lines file to `s3://<audit-bucket>/<YYYY>/<MM>/<DD>.jsonl.gz`. Bucket has object-lock for write-once tamper-resistance.

## Acceptance criteria

- [ ] Every state-changing request creates an `audit_log` row with the documented fields.
- [ ] Failed authentication attempts (wrong password, wrong TOTP) appear in the audit log with the correct outcome.
- [ ] DLQ entries in the ingest worker (#07 + #08) also write audit-log rows.
- [ ] The daily S3 mirror runs and produces a tamper-resistant file (object lock retains it).
- [ ] A test that issues a known sequence of requests and asserts the audit_log rows match by `correlation_id`.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 03 (HTTP API + first state-mutating endpoint).
