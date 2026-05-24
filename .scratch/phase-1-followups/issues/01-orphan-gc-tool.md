# Issue 01 — Postgres `devices` orphan GC tool

Status: ready-for-agent
Type: AFK
Estimate: 2–3 hr

## Parent

- Wave 0 handoff observation (no orphans currently; failure window is real but unhit).
- ADR-018 (Fargate workers, not Lambda) — applies to one-shot batch jobs too via ADR-023.
- ADR-023 (Fargate scheduled tasks for batch jobs) — same pattern as `audit-mirror`, except this one is operator-invoked, not scheduled.

## What to build

A one-shot Go binary at `cmd/cp-cleanup-orphans/` that finds and (optionally) deletes `devices` rows whose IoT thing or certificate no longer exists. Closes the "IoT provisioning succeeded → Postgres INSERT failed" window: `registry.Enroll` returns before INSERT when IoT provisioning fails, but the IoT-success / DB-fail leg has no current cleanup.

Scope:

- New `cmd/cp-cleanup-orphans/` package. Reuse `internal/cp/storage` for the pgxpool + DSN loading; reuse the AWS SDK clients already in `internal/cp/iotprovisioner` for IoT calls.
- Default mode: `--dry-run` (the safe default). Report orphans by `device_id`, `thing_name`, `cert_id`, and which side is missing.
- Apply mode: `--apply` deletes orphan Postgres rows. Does NOT delete IoT-side residue — that's a separate operational step (the manual `aws iot delete-thing` flow Wave 0 used was correct for IoT residue).
- Run as a one-off ECS task with the existing `cp-api` task role (already has the needed IoT read perms). No new Terraform resources beyond a task-definition family; no EventBridge schedule (operator-invoked).
- Tests follow ADR-012: integration test with motoserver for IoT (per memory `project_iot_mock_choice`), real Postgres (testcontainers).

Out of scope:

- IoT-side orphan cleanup (separate slice if/when it becomes a recurring problem; today it's a once-per-incident manual step).
- Automatic scheduling. Operator runs it when needed; if the volume of orphans grows we revisit.

## Acceptance criteria

- [ ] `cmd/cp-cleanup-orphans/main.go` exists with `--dry-run` (default) and `--apply` flags, plus structured slog output following ADR-011 (correlation_id stamped).
- [ ] Integration test exercises both legs (Postgres orphan present / IoT orphan present) against moto + testcontainers Postgres.
- [ ] A Terraform task-definition family `uknomi-cp-cleanup-orphans` exists alongside `audit-mirror` under `infra/terraform-deploy/`, using the `cp-api` task role.
- [ ] `docs/runbooks/orphan-gc.md` documents `aws ecs run-task` invocation + how to interpret output.
- [ ] `docs/architecture.md` § Modules lists the new binary.

## Blocked by

- None. The bench enrollment already exercised every code path this tool depends on.

## Notes

- The handoff verified there are no orphans currently (post Wave-0 cleanup). The cost/benefit of building this is "low-rate insurance"; defer until either an orphan is observed or the fleet grows past Wave 1.
- Per memory `feedback_tdd_commit_cadence`: build TDD red→green with a commit per cycle.
