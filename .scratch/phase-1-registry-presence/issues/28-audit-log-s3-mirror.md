# Issue 28 — Daily audit-log S3 mirror

Status: ready-for-agent
Type: AFK

## Parent

- [Issue #20](./20-audit-log-surface.md) — landed the `audit_log` Postgres table + `audit.Writer` surface. The daily S3 mirror was the one AC deferred to a follow-on.
- Architecture: `docs/architecture.md` § Security — "Append-only audit log in Postgres + daily S3 mirror."
- Existing infra: `infra/terraform-deploy/s3.tf` already created the `audit-mirror` bucket (#25); object-lock + the writer task are what this slice adds.

## What to build

A daily export of the prior day's `audit_log` rows to S3 as a gzipped JSON Lines file, with object-lock for tamper-evidence.

Scope:

- **Exporter binary or goroutine.** Decide between (a) a new short-lived Fargate task (`cmd/audit-mirror`) on an EventBridge schedule, or (b) a goroutine inside `cmd/cp-ingest` that fires at 00:05 UTC daily. Recommend (a) — clean failure isolation, separate IAM, easier rollback. Decision lives in this issue's grilling.
- **Export format.** One JSON object per audit_log row, one row per line, gzipped, written to `s3://<audit-bucket>/<YYYY>/<MM>/<DD>.jsonl.gz`. Use the `at` column UTC date for the partition keys. Idempotent: if the file already exists, the exporter overwrites only if no object-lock retention applies.
- **Object-lock.** Add `object_lock_configuration` to the audit-mirror bucket in `infra/terraform-deploy/s3.tf` with compliance-mode retention (e.g. 7 years per legal). New objects inherit the retention horizon automatically.
- **IAM.** A new task role with `s3:PutObject` + `s3:GetObject` on the audit-mirror bucket, and `SELECT` on audit_log via the same DB user cp-ingest uses (or a read-only role — TBD in the grilling).
- **Alarming.** CloudWatch alarm on the exporter task's exit code; SNS notification on failure.
- **Backfill.** A one-shot CLI flag (`--from YYYY-MM-DD --to YYYY-MM-DD`) to mirror historical rows the first time the job runs.

Out of scope:

- Full SIEM integration (just S3 export; downstream consumers ingest the JSONL files).
- Cross-region replication of the audit bucket (Phase 2 hardening).
- Streaming export (the daily batch is fine for Phase 1's row volume).

## Acceptance criteria

- [ ] The exporter runs daily at 00:05 UTC and produces `s3://<audit-bucket>/<YYYY>/<MM>/<DD>.jsonl.gz` containing every audit_log row whose `at` falls on the prior day (UTC).
- [ ] The audit-mirror bucket has object-lock enabled with the agreed retention horizon; new objects inherit it.
- [ ] The exporter's IAM role is scoped to `s3:PutObject` on the audit-mirror bucket and the audit_log SELECT path only.
- [ ] CloudWatch alarm fires + SNS publishes if the export task fails or does not run for 25+ hours.
- [ ] A backfill flag mirrors a historical range.
- [ ] Integration test against testcontainers Postgres + LocalStack S3 (per `[[project_iot_mock_choice]]`: LocalStack is fine for non-IoT services).
- [ ] **Documentation updated.** `docs/architecture.md` reflects the exporter's role; if the Fargate-task-vs-goroutine choice is load-bearing, capture it as an ADR.

## Blocked by

- None on the code side. Operationally depends on Issue 20 being deployed (audit_log table populated).

## Notes

- Decision worth grilling: Fargate scheduled task (separate failure domain, separate IAM, clean rollback) vs. goroutine in cp-ingest (one less service to operate). Recommend the former; capture the rationale in an ADR if the discussion uncovers non-obvious trade-offs.
- Retention horizon for object-lock: legal/compliance owner picks the years. PRD says "long-term retention" without a number; needs a human decision before apply.
