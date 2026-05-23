# ADR-023: Fargate scheduled tasks for periodic batch jobs

**Status:** Accepted (2026-05-23)

**Context.** Issue 28 introduces the first periodic batch job in the CP: a daily export of audit_log rows to S3. Two shapes presented themselves:

1. **A goroutine inside an existing service** (most plausibly `cp-ingest`). The job fires off a timer or cron loop in-process; one less service to operate.
2. **A short-lived Fargate task on an EventBridge schedule.** The same launch primitive ECS already runs; one container start per execution; explicit IAM identity per job.

Both are workable. The trade-off is operational topology, not raw capability.

The Phase 1 stack already runs `cp-ingest` as a long-lived Fargate service. Adding the audit-mirror job as a goroutine inside it would couple two unrelated failure surfaces: a stuck mirror upload would consume one of cp-ingest's slots; an OOM in cp-ingest's heartbeat path would kill the mirror task mid-export. The mirror's IAM identity would have to be the same as cp-ingest's — a permission-creep we want to avoid (the mirror needs `s3:PutObject` on the audit bucket; cp-ingest does not).

The Fargate-scheduled-task path costs one new ECS task definition + one EventBridge rule + one IAM role. None of those is novel — they reuse the same patterns the long-lived services use.

The choice is load-bearing because **future batch jobs (Phase 2 metric rollups, agent-binary signing job, etc.) will follow whichever pattern is set here.** If the audit-mirror goroutine path is chosen and the next two jobs follow suit, `cp-ingest` becomes a kitchen-sink service with five unrelated goroutines and a confused IAM policy.

**Decision.** Periodic batch jobs in the CP are implemented as **short-lived Fargate tasks launched by EventBridge on a cron schedule**, not as goroutines in long-lived services.

Concretely:

- Each job has its own `cmd/<job>/main.go` binary, its own Dockerfile, its own ECR repo, its own task definition, its own task IAM role.
- Each job runs as a one-shot: the binary exits as soon as its work is done; non-zero exit codes propagate to the ECS stopped-task event and to CloudWatch.
- EventBridge holds the schedule (`cron(...)`); the EventBridge invoke role has `ecs:RunTask` scoped to the specific task definition + cluster.
- Per-job CloudWatch alarms cover (a) explicit failure log lines and (b) missed schedules (no completion line in 1.04× the schedule interval).

The pattern is exercised first by `cmd/audit-mirror` (Issue 28). Future jobs inherit the shape.

**Consequences.**

- (+) Each job has an independent failure domain. A buggy mirror upload does not destabilize cp-ingest.
- (+) Each job's IAM role is scoped to exactly what that job does. No accidental permission creep.
- (+) Restart / rollback is per-job: roll back one job's image without touching the long-lived services.
- (+) Logs land in a dedicated log group per job. Grep + the per-job alarm runbooks are decoupled.
- (+) EventBridge's failure semantics (DLQ on RunTask failure, optional retry policy) are usable without redesigning the in-process scheduler.
- (-) One more task definition + EventBridge rule per job. Roughly 80 lines of HCL each — acceptable.
- (-) Each job's container start adds a few seconds of latency on top of the actual work. Acceptable for batch jobs scheduled at human-relevant cadences (daily, hourly).
- (-) A one-shot Fargate task carries no persistent state between runs. Jobs that need cross-run state (e.g., a "last successful watermark") store it in Postgres or S3 explicitly, not in memory. This is the right discipline anyway.

**Verification.** `N/A — environmental/infra decision`. Verified operationally by:

- The presence of `cmd/audit-mirror/` (or future `cmd/<job>/`) as a standalone binary, not a function in `cp-api` / `cp-ingest`.
- The corresponding `aws_ecs_task_definition.<job>` + `aws_cloudwatch_event_rule.<job>_*` resources in `infra/terraform-deploy/`.
- A per-job task IAM role distinct from the long-lived services' task roles.
- Each job's alarm set follows the failure + stale-completion pair documented for audit-mirror in [`docs/runbooks/alarms/audit-mirror.md`](../runbooks/alarms/audit-mirror.md).
