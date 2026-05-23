# Alarms — `uknomi-cp-audit-mirror-failure` / `uknomi-cp-audit-mirror-stale`

Two alarms cover the audit-log S3 mirror (ADR-023, Issue 28); both publish to the same SNS topic and share a runbook because the triage paths overlap.

## `uknomi-cp-audit-mirror-failure`

**Fires when**: any `"audit-mirror failed"` log line lands in the audit-mirror log group over a 5-minute window. The exporter emits this from its top-level error path before exiting non-zero.

## `uknomi-cp-audit-mirror-stale`

**Fires when**: no `"audit-mirror completed"` line in the past 25 hours (25 consecutive 1-hour evaluation periods with zero completions). The job runs once per day at 00:05 UTC; 25 hours of slack catches a missed schedule before the next one is due.

## What to check first

1. **Recent run status.**
   ```bash
   aws logs tail /uknomi-cp/audit-mirror --since 36h
   ```
   - Look for the `"audit-mirror completed"` success line. If you see it within the last 24h, the stale alarm is a CloudWatch evaluation lag — wait one more period.
   - Look for an `"audit-mirror failed"` line + the `err` attribute. The most common errors:
     - **`put 2026/...jsonl.gz: AccessDenied`** — the audit-mirror task role lost `s3:PutObject`, or someone tightened the bucket policy. Check the IAM role + bucket policy.
     - **`pgxpool: ...`** — the `db-dsn` secret is wrong (drift from RDS rotation) or RDS is unreachable.
     - **`head ... AccessDenied`** with object-lock retention error — the day's object exists and is locked; running the job again is a no-op via the idempotency short-circuit, so this should never fire under normal use. If it does, someone bypassed the idempotency check.

2. **Confirm the schedule fired.**
   ```bash
   aws events describe-rule --name uknomi-cp-audit-mirror-daily
   aws ecs list-tasks --cluster uknomi-cp --family uknomi-cp-audit-mirror --desired-status STOPPED --max-results 5
   ```
   If `list-tasks` shows no recent stopped tasks, EventBridge isn't firing — check the rule's `State` (`ENABLED` vs `DISABLED`) and the EventBridge invoke role's `ecs:RunTask` permission.

3. **Recover the missing day.**
   The exporter idempotency short-circuits days that already have an object. To run a single missed day manually:
   ```bash
   aws ecs run-task \
     --cluster uknomi-cp \
     --task-definition uknomi-cp-audit-mirror \
     --launch-type FARGATE \
     --network-configuration "awsvpcConfiguration={subnets=[<private>],securityGroups=[<tasks-sg>],assignPublicIp=DISABLED}" \
     --overrides '{"containerOverrides":[{"name":"audit-mirror","command":["--date","2026-05-22"]}]}'
   ```
   For a multi-day backfill, use `--from / --to` instead.

## Escalation

- **For a one-day miss after a successful prior run**: trigger the missed day manually as above. No further action.
- **For repeated failures (3+ days)**: roll back the most recent audit-mirror image (`-var image_tag=<prior-sha>` and `terraform apply`). The job will resume on the next 00:05 UTC; backfill the gap once recovered.
- **For an `AccessDenied` on PutObject that doesn't have an obvious cause**: a bucket policy change is the most likely culprit. Check who touched it (`aws s3api get-bucket-policy --bucket <name>` + CloudTrail).
- **For `head ... AccessDenied` from object-lock**: the bucket's governance-mode retention permits the operator (with the right IAM permissions) to override. Talk to whoever set up the lock before bypassing — there may be a compliance reason for the day's content to stay immutable.

## Re-exporting a day

The daily output is locked by 1y governance-mode retention. To overwrite a day deliberately:

```bash
# Set the bypass-governance header on the next put (requires
# s3:BypassGovernanceRetention on the IAM identity).
aws s3api delete-object \
  --bucket <audit-mirror-bucket> --key 2026/05/22.jsonl.gz \
  --bypass-governance-retention

# Then re-run the day with the manual ECS run-task above.
```

This is a rare ops action — document any use in the audit-log itself.
