# Alarm — `uknomi-cp-health-probes-dlq`

**Fires when**: any message lands in `uknomi-cp-health-probes-dlq` (`ApproximateNumberOfMessagesVisible > 0` for a 60s window). Same shape as the `uknomi-cp-service-status-dlq` alarm; root-cause investigation overlaps.

**Why it matters**: the health-probes queue carries each device's 5-minute `healthprobes.Report`. A DLQ'd message means cp-ingest's `HealthProbeIngester` rejected a report as poison — that device's Health panel goes stale, so the proactive visibility #19 exists to provide is silently lost for that device.

**Known causes** (ordered by likelihood):

1. **Unknown device** — a report from a `device_id` not in the `devices` table (decommissioned mid-flight, or a report queued in MQTT retention from before a re-enroll). `RecordHealthProbes` returns `ErrDeviceNotFound` → ingester poisons it → DLQ. Benign; safe to drain.
2. **Empty `device_id`** — a malformed publish. The ingester poisons reports with no device_id. Benign individually, but a cluster suggests an agent or IoT-Rule bug.
3. **Schema drift** — the agent emits a `healthprobes.Report` shape the consumer can't decode (e.g. a probe protocol change shipped to agents before cp-ingest). Real bug. The probe-name constants are shared between agent and ingest (ADR-034) precisely to prevent this.
4. **Decode failure on the body** — message body not valid JSON; the consumer rejects pre-handler. Real bug in the IoT Rule SQL or transport.

## What to check first

1. **Pull the DLQ'd message(s)** (inspect before draining):
   ```bash
   aws sqs receive-message \
     --queue-url "$(aws sqs get-queue-url --queue-name uknomi-cp-health-probes-dlq --query QueueUrl --output text)" \
     --max-number-of-messages 10 --visibility-timeout 0 \
     --message-attribute-names All --attribute-names All
   ```
   Look at `device_id` + `correlation_id` in the body → does the device still exist?

2. **Pair with the cp-ingest log:**
   ```bash
   aws logs tail /uknomi/cp-ingest --since 30m \
     --filter-pattern '"audit.message_rejected"' --format short
   ```
   Find the rejection adjacent to the DLQ'd `correlation_id` for the cause.

3. **Cross-check the device exists:**
   ```sql
   SELECT id, hostname, enrolled_at FROM devices WHERE id = '<device_id>';
   ```
   Missing → "unknown device" path (benign, drain).

## Drain procedure

When the cause is benign (decommissioned/unknown device, isolated malformed publish):

```bash
QURL="$(aws sqs get-queue-url --queue-name uknomi-cp-health-probes-dlq --query QueueUrl --output text)"
# Repeat for each DLQ'd message
aws sqs delete-message --queue-url "$QURL" --receipt-handle '<receipt-from-receive-message>'
```

The alarm transitions back to OK on the next 60s window once the queue is empty.

## When to escalate

- DLQ refills within minutes of draining → real bug (schema drift, decode failure). Snapshot messages first, then dig in `internal/cp/ingest/healthprobes.go` / `internal/protocol/healthprobes/`.
- Same `correlation_id` re-appears after delete → poison-handling busted; check `internal/cp/sqsconsumer/`.
- Hundreds of messages → systemic (cp-ingest down, broken deploy). Check ECS service health first.

## Related

- PRD: `.scratch/phase-2-fleet-health-probes/PRD.md`
- [ADR-034](../../adr/0034-agent-backend-abstraction-os-agnostic-surface.md), ADR-018 (Fargate ingest + DLQ poison path)
- [health-probe-red.md](health-probe-red.md) — the per-probe-type red alarms
- `uknomi-cp-service-status-dlq` — identical-shape DLQ alarm
