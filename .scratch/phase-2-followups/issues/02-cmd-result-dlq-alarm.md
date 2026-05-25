# Issue 02 — CloudWatch alarm on `uknomi-cp-cmd-result-dlq`

Status: ready-for-agent
Type: AFK
Estimate: 30 min

## Parent

- Source: [Phase 2 slice 2 PRD](../../phase-2-allow-list-overrides/PRD.md) deferred this ("A `cmd-result.config-update.error` log filter can be added later if rollout proves noisy"). Now that slice 2 is live, the operational risk of a silent DLQ growth is real and the alarm cost is trivial.
- ADRs to honour: ADR-021 (CloudWatch alarms route through the existing `uknomi-cp-alarms` SNS topic; per-alarm runbook under `docs/runbooks/alarms/`).

## What to build

Mirror the existing `uknomi-cp-service-stopped` + presence DLQ alarms for the new `uknomi-cp-cmd-result` pipeline.

### Scope

- New `aws_cloudwatch_metric_alarm` `uknomi-cp-cmd-result-dlq-non-empty` in `infra/terraform-deploy/alarms.tf`:
  - Namespace: `AWS/SQS`
  - Metric: `ApproximateNumberOfMessagesVisible`
  - Dimension: `QueueName = uknomi-cp-cmd-result-dlq`
  - Threshold: `> 0` for 1 datapoint of 5 min — DLQ should ALWAYS be empty; any message means a config.update ACK that the handler couldn't process (unknown device, malformed envelope).
  - Alarm actions: existing `uknomi-cp-alarms` SNS topic.
  - OK actions: same topic.
- New runbook `docs/runbooks/alarms/cmd-result-dlq.md` explaining: known causes (device decommissioned mid-config.update; envelope schema drift between agent and CP), drain procedure, when to escalate.

## Acceptance criteria

- [ ] Terraform plan + apply against deploy-root surfaces only the new alarm + runbook (no drift).
- [ ] Alarm fires in OK state (queue is empty) within ~10 min of apply.
- [ ] Synthetic test: send a deliberately-poisoned message to the queue (manually via `aws sqs send-message`); confirm cp-ingest DLQs it; confirm the alarm transitions to ALARM within the 5-min window; drain + transition back to OK.
- [ ] **Documentation updated.** Runbook exists; alarm row appears in the alarm table in `architecture.md` if one exists, otherwise document under § Observability.

## Blocked by

- None.

## Related future work

A symmetric alarm on `uknomi-cp-service-status-dlq` should exist (likely already does — verify; if not, add as a sub-issue here). The cmd-result alarm is more urgent because slice 2 is the first place cp-ingest's cmd-result handler runs in production, so a regression here is fresh territory.
