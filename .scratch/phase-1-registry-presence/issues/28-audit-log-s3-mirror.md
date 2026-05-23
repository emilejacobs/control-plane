# Issue 28 — Daily audit-log S3 mirror

Status: done
Type: AFK
Completed: 2026-05-23 — 6-cycle TDD slice; pattern captured as ADR-023.

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

- [x] The exporter runs daily at 00:05 UTC and produces `s3://<audit-bucket>/<YYYY>/<MM>/<DD>.jsonl.gz` containing every audit_log row whose `at` falls on the prior day (UTC). EventBridge rule + ECS RunTask target wire in `infra/terraform-deploy/audit-mirror.tf`.
- [x] The audit-mirror bucket has object-lock enabled with the agreed retention horizon; new objects inherit it. 1-year governance-mode per the user's call. `infra/terraform-deploy/s3.tf` carries `object_lock_enabled = true` (audit-mirror only) + `aws_s3_bucket_object_lock_configuration.audit_mirror` with mode=GOVERNANCE, days=365.
- [x] The exporter's IAM role is scoped to `s3:Put/Get/List` on the audit-mirror bucket only. `db-dsn` Secrets Manager read comes from the shared task-execution role's existing `uknomi/cp/*` policy via the task-def `secrets` injection; the audit-mirror task role does not get raw Secrets Manager access.
- [x] CloudWatch alarms fire on failure + on a 25-hour gap. Two log-metric-filter + alarm pairs (`audit-mirror-failure`, `audit-mirror-stale`); shared runbook at [`docs/runbooks/alarms/audit-mirror.md`](../../../docs/runbooks/alarms/audit-mirror.md).
- [x] A backfill flag mirrors a historical range. `cmd/audit-mirror --from YYYY-MM-DD --to YYYY-MM-DD` drives `Exporter.ExportRange`; integration test seeds three days of rows and asserts one object per UTC day in the range, with the out-of-range day skipped.
- [x] Integration test against testcontainers Postgres + moto S3. Per `[[project_iot_mock_choice]]`, moto handles S3 fine for non-IoT services without a second LocalStack instance.
- [x] **Documentation updated.** [ADR-023](../../../docs/adr/0023-fargate-scheduled-tasks-for-batch-jobs.md) captures the Fargate-scheduled-task vs goroutine choice as the pattern for future batch jobs. `docs/architecture.md` mentions the audit-mirror flow. CONTEXT.md not touched — no new domain terms (audit-log mirror is described by `[[audit_log]]` + S3, both pre-existing).

## Blocked by

- None on the code side. Operationally depends on Issue 20 being deployed (audit_log table populated) and #26's CI run pushing the audit-mirror image to ECR.

## Notes

- Decision worth grilling: Fargate scheduled task vs goroutine in cp-ingest — captured as ADR-023.
- Retention horizon for object-lock: user picked 1-year governance. Adjustable later via root-account override (governance, not compliance).

### Completion notes (2026-05-23)

6 cycles, `c926c63` → this commit:

1. `c926c63` — TDD: `auditmirror.Exporter.ExportDate` writes gzipped JSONL to `s3://<bucket>/YYYY/MM/DD.jsonl.gz`. Integration test against testcontainers Postgres + moto S3.
2. `b728727` — TDD: `ExportDate` is idempotent. HeadObject short-circuit avoids the AccessDenied that governance-mode object-lock would throw on a re-PUT.
3. `b80851b` — TDD: `Exporter.ExportRange` iterates UTC days in `[from, to]`. Three-day test (one in-range, one empty, one outside) verifies object set.
4. `79e606f` — `cmd/audit-mirror` binary + Dockerfile. Flags: `--date`, `--from`/`--to`; no-flag default exports yesterday. Static 14MB linux/amd64 ELF.
5. `51101a6` — Terraform infra: ECR repo (`uknomi/audit-mirror`), task role scoped to s3-on-audit-mirror-only, ECS task definition, EventBridge cron rule (00:05 UTC daily), EventBridge invoke role with `ecs:RunTask` + `iam:PassRole`, two CloudWatch alarms, plus `object_lock_enabled = true` + `aws_s3_bucket_object_lock_configuration` on the audit-mirror bucket. CI image-build workflow gains a 4th matrix entry.
6. This commit — ADR-023 (Fargate scheduled tasks for batch jobs), audit-mirror alarm runbook, alarms README updated, architecture.md updated, Issue 28 closed.

### Out of scope (filed as follow-ons if/when needed)

- **Cross-region replication of the audit bucket.** Phase 2 hardening — out of scope until DR review.
- **SIEM integration.** The S3 mirror is the export surface; downstream consumers ingest the JSONL files at their own cadence.
- **Per-job DLQ on EventBridge RunTask failures.** Phase 1 relies on the stale-completion alarm; if missed schedules become common, add an EventBridge target DLQ + SNS subscription.
