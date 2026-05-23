# Alarm — `uknomi-cp-sweeper-lag`

**Fires when**: the `cp-ingest` PresenceSweeper has not emitted a `sweeper.tick` log line in the last 2 minutes (two consecutive 60-second evaluation periods with zero ticks).

**Why it matters**: the sweeper is the backstop for ADR-018's lifecycle fast-path. The fast-path catches a clean disconnect via IoT Core's lifecycle queue; the sweeper catches the dirty disconnect (process killed, network blackholed, host yanked). A stuck sweeper goroutine means devices that died ungracefully appear online indefinitely.

## What to check first

1. **Is `cp-ingest` running?**
   ```bash
   aws ecs describe-services --cluster uknomi-cp --services cp-ingest \
     --query 'services[0].{desired:desiredCount,running:runningCount,events:events[:3]}'
   ```
   - `runningCount = 0` → check `events[]` for image-pull failures, task-role denial, or out-of-memory kills.
   - `desiredCount = 0` → someone scaled it down deliberately; the image-flip slice raised it to 1.

2. **Recent sweeper logs.**
   ```bash
   aws logs tail /uknomi-cp/cp-ingest --since 10m --filter-pattern '"sweeper.tick"'
   ```
   Empty output for the full window confirms the sweeper has actually stopped emitting. If you see ticks but they're slower than 30s apart, ADR-018's sweeper interval (30s) has changed — verify against `internal/cp/ingest/sweeper.go`.

3. **Process state inside the task.**
   The sweeper is a goroutine inside `cp-ingest`'s `Run`. If `cp-ingest` is running but the sweeper isn't ticking, the goroutine has deadlocked or panicked silently — `sqsconsumer.invoke` recovers panics in the message handlers but a panic in the sweeper itself escapes. Tail logs for `runtime error`, `panic:`, or `failed to persist sweep transition` storms.

## Escalation

- If the task is healthy but no ticks for 5+ minutes: force-redeploy `cp-ingest` (`aws ecs update-service --cluster uknomi-cp --service cp-ingest --force-new-deployment`). The 30s interval means a fresh task starts ticking within one cycle.
- If force-redeploy doesn't recover within 2 minutes: roll back the most recent cp-ingest image (`-var image_tag=<prior-sha>` in `infra/terraform-deploy/` and `terraform apply`).
- If the lag fired during Wave 0 hardware testing, page the on-bench engineer — the device list on the dashboard will start lying about online-ness.
