# Issue 02 — Empty deploy-root log group cleanup

Status: done (2026-05-24 — landed in the same commit that filed this issue, `2c0ac41`)
Type: AFK
Estimate: 15 min

## Parent

- Wave 0 handoff follow-on #4.
- The cleanup was started under the auto-deploy slice's tail but parked when the team pivoted to Phase 2. The Terraform edit was reverted; current `main` still creates the empty group.

## What to build

Drop `cp-ingest` from `local.services` in [`infra/terraform-deploy/ecs.tf`](../../../infra/terraform-deploy/ecs.tf) so the unused `/uknomi-cp/cp-ingest` log group is destroyed. The real cp-ingest log group is `/uknomi/cp-ingest`, managed by [`infra/terraform/modules/cp-ingest-service`](../../../infra/terraform/modules/cp-ingest-service/main.tf). The deploy-root group has zero streams + zero bytes (verified 2026-05-24 via `aws logs describe-log-groups`).

Scope:

- One-line removal in `ecs.tf`, plus a `# cp-ingest omitted because …` comment.
- Update the explanatory comment in [`infra/terraform-deploy/alarms.tf`](../../../infra/terraform-deploy/alarms.tf) on the `sweeper_tick` log-metric filter to remove the stale reference to `aws_cloudwatch_log_group.service["cp-ingest"]`.
- `terraform plan` should show exactly `1 to destroy` (the log group) and `Changes to Outputs` removing `cp-ingest` from `service_log_group_names`.

Out of scope:

- Any change to where cp-ingest actually logs. It already writes to `/uknomi/cp-ingest`; nothing changes operationally.

## Acceptance criteria

- [ ] `local.services` in `ecs.tf` no longer contains `cp-ingest`.
- [ ] `alarms.tf` comment block on `sweeper_tick` no longer references `.service["cp-ingest"]`.
- [ ] `terraform fmt + validate` clean.
- [ ] `terraform plan` shows exactly `0 to add, 0 to change, 1 to destroy` (the log group) plus the `service_log_group_names` output diff.
- [ ] `terraform apply` (operator gate) destroys the empty group. Verified via `aws logs describe-log-groups --log-group-name-prefix /uknomi-cp/cp-ingest` returning empty.

## Blocked by

- None.

## Notes

- Before the apply, re-verify `streams = 0, bytes = 0` on `/uknomi-cp/cp-ingest`. If a stream appeared (no reason it should), investigate before destroying.
- The handoff also flagged this required verifying nothing else references the group. That verification was done in the parked session: only the now-stale comment in `alarms.tf` mentions it; the sweeper-tick filter already targets `module.cp_ingest.log_group_name`.
