# Issue 20 — Audit log surface

Status: done
Type: AFK
Completed: 2026-05-23 — 8-cycle TDD slice; daily S3 mirror split to Issue 28.

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

- [x] Every state-changing request creates an `audit_log` row with the documented fields. Verified end-to-end in `TestAuditLogEndToEnd` (three POSTs → three rows; the `HTTPMiddleware` envelope is suppressed when the handler audited).
- [x] Failed authentication attempts (wrong password, wrong TOTP) appear in the audit log with the correct outcome. Verified in `auth.TestLoginFailureWritesAuditEntry` (unit) and `TestAuditLogEndToEnd` (integration: payload->>'reason' = 'invalid_credentials').
- [x] DLQ entries in the ingest worker (#07 + #08) also write audit-log rows. `sqsconsumer.toDLQ` writes through the Writer; covered by `TestConsumerDLQEmitsAuditRowAndSlogLine`.
- [ ] **The daily S3 mirror runs and produces a tamper-resistant file (object lock retains it).** Deferred to follow-on **Issue 28**. The S3 audit-mirror bucket from #25 already exists; #28 will add the Fargate cron task + the object-lock policy.
- [x] A test that issues a known sequence of requests and asserts the audit_log rows match by `correlation_id` — `TestAuditLogEndToEnd`.
- [x] **Documentation updated.** `docs/architecture.md` § Modules updated (audit_log now built; daily S3 mirror called out as #28). `docs/CONTEXT.md` not touched — no new domain terms (audit_log + audit event are PRD-defined and self-explanatory). No new ADR — the design follows PRD § audit_log + ADR-011's correlation_id contract; no load-bearing surprises emerged.

## Blocked by

- Issue 03 (HTTP API + first state-mutating endpoint). ✅ Done before this slice started.

### Completion notes (2026-05-23)

8 TDD cycles (`f957947` → `f062343`), each committed independently per the TDD-commit-cadence memory:

1. `f957947` — tracer bullet: `audit.Writer` interface, `Entry` struct, `MemoryWriter`, correlation_id capture via `cplog.WithCorrelationID`.
2. `181309b` — slog co-emission in the legacy flat-attr shape (so the 34 existing `audit.*` assertions stay green when handlers migrate).
3. `5d919d0` — `auth.LoginHandler` is the first explicit consumer (`NewLogin(svc, audit.Writer)`); unit test asserts the structured Entry.
4. `30ce5c6` — sweep of the rest: `FirstRun`, `Refresh`, `TotpEnroll`, `Enrollment`, `sqsconsumer.Consumer`. `audit.Discard` renamed to `audit.SlogOnly` (keeps slog co-emission). `sqsconsumer.toDLQ` installs `c.log` into the audit ctx so the slog line lands on the consumer's configured logger.
5. `25cd216` — `audit.HTTPMiddleware` for envelope auto-write with handler-suppresses-middleware contract (the `markHandled(ctx)` tracker). Three unit tests lock fall-through, suppression, and read-method-skip.
6. `13ddb4f` — migration `010_audit_log.sql` + `audit.PostgresWriter`. Two integration tests cover full-field round-trip + nil-Payload→'{}' edge case.
7. `2c05852` — wire `PostgresWriter` into `cmd/cp-api` + `cmd/cp-ingest`.
8. `f062343` — `TestAuditLogEndToEnd` + bug fix in `cplog.Middleware` (now also stamps `WithCorrelationID` alongside `WithLogger`, the gap cycle 1 left for the middleware to fill).

### Out of scope (filed as a follow-on)

**Issue 28 — Daily audit-log S3 mirror.** The audit-mirror bucket from #25 already exists; #28 layers:
- A Fargate scheduled task (or a goroutine in cp-ingest) that exports the prior day's audit_log rows as a gzipped JSON Lines file to `s3://<audit-bucket>/<YYYY>/<MM>/<DD>.jsonl.gz`.
- Object-lock policy on the bucket (compliance-mode retention for tamper-evidence).
- Alarm on the export job failing.
