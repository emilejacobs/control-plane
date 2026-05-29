# Alarm runbooks

Every CloudWatch alarm in `infra/terraform-deploy/alarms.tf` carries an `alarm_description` that points at the matching file here. The SNS-fed pager / email surface includes the description verbatim, so an on-call landing in this directory should already know which file to open.

## #25 baseline alarms

| Alarm | Trigger | Runbook |
|---|---|---|
| `uknomi-cp-alb-5xx` | > 5 target 5xx in 5 min | TODO |
| `uknomi-cp-rds-cpu` | RDS CPU > 80% for 10 min | TODO |
| `uknomi-cp-rds-free-storage` | RDS free storage < 2 GB | TODO |
| `uknomi-cp-cp-api-running-count` | cp-api running tasks < 1 for 5 min | TODO |
| `uknomi-cp-dashboard-running-count` | dashboard running tasks < 1 for 5 min | TODO |
| `uknomi-cp-heartbeat-dlq` | Heartbeat DLQ non-empty | TODO |
| `uknomi-cp-lifecycle-dlq` | Lifecycle DLQ non-empty | TODO |

## #21 log-derived alarms

| Alarm | Trigger | Runbook |
|---|---|---|
| `uknomi-cp-sweeper-lag` | No `sweeper.tick` in 2 min | [sweeper-lag.md](sweeper-lag.md) |
| `uknomi-cp-login-failure-spike` | > 100 `audit.login` failures in 5 min | [login-failure-spike.md](login-failure-spike.md) |
| `uknomi-cp-enrollment-ratelimit-trip` | Any `ratelimit.trip` line in 5 min | [enrollment-ratelimit-trip.md](enrollment-ratelimit-trip.md) |
| `uknomi-cp-hostname-anomaly` | Any `audit.enrollment.anomaly` in 5 min | [hostname-anomaly.md](hostname-anomaly.md) |

## #28 audit-mirror alarms

| Alarm | Trigger | Runbook |
|---|---|---|
| `uknomi-cp-audit-mirror-failure` | Any `"audit-mirror failed"` log line in 5 min | [audit-mirror.md](audit-mirror.md) |
| `uknomi-cp-audit-mirror-stale` | No `"audit-mirror completed"` line in 25 hours | [audit-mirror.md](audit-mirror.md) |

## #19 fleet health probes

| Alarm | Trigger | Runbook |
|---|---|---|
| `uknomi-cp-health-probe-<probe>` (×7) | A probe red on ≥1 device for ≥15 min | [health-probe-red.md](health-probe-red.md) |
| `uknomi-cp-health-probes-dlq` | Health-probes ingest DLQ non-empty | [health-probes-dlq.md](health-probes-dlq.md) |

The seven per-probe alarms (`auto_login`, `gui_session`, `plate_recognizer_container`, `plate_recognizer_config`, `usb_audio`, `whisper_model`, `boot_sanity`) share one runbook with a per-probe section.

The #25-baseline runbooks are marked TODO — they were built before this directory existed and were never written up. File an issue or fold them into the next on-call shadow shift.

## Verification

Per the Issue 21 AC: each alarm needs to fire once against a deployed environment so we know the wiring is real. The fastest path:

- **DLQ alarms**: post a JSON message directly to the queue (`aws sqs send-message --queue-url <heartbeat-dlq-url> --message-body "{}"`).
- **Sweeper lag**: kill the cp-ingest task (`aws ecs update-service --cluster uknomi-cp --service cp-ingest --desired-count 0`). The alarm fires within 2 min. Restore desired-count to 1 to clear.
- **Login failure spike**: 101 `curl` requests with a wrong password against `/auth/login` over a couple of minutes.
- **Enrollment rate-limit trip**: 21 `curl` requests with junk JSON against `/enrollments` from the same source IP within an hour.
- **Hostname anomaly**: one `/enrollments` with a hostname like `not-a-valid-name`.

Document the fire-test runs in the Wave 0 bench runbook so they happen once and don't slip.
