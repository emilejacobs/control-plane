# Alarm — `uknomi-cp-cmd-result-dlq`

**Fires when**: any message lands in `uknomi-cp-cmd-result-dlq` (i.e. `ApproximateNumberOfMessagesVisible > 0` for a 60s window). Same-shape alarm exists for `uknomi-cp-service-status-dlq`; root-cause investigation overlaps.

**Why it matters**: the cmd-result queue carries the agent → CP ACKs for every Phase 2+ downward command (`config.update`, `log.tail`, plus the Phase 0 `service.status` / `service.restart` results). A DLQ'd message means cp-ingest's `CmdResultIngester` couldn't process the agent's response — the operator who initiated the cmd (or whose ACK matches a since-deleted request row) will see "pending" forever on the dashboard.

**Known causes** (ordered by likelihood):

1. **Stale request row swept early** — log-tail rows expire at 24h via `LogTailSweeper`. If the agent ACKs a request older than 24h, `CompleteLogTail`/`FailLogTail` return `ErrLogTailNotFound` → ingester treats as poison → DLQ. Benign; safe to drain. *Frequency: rare unless the agent is offline for 24h+ with a queued cmd in MQTT retention.*

2. **Schema drift between agent and cp-ingest** — agent emits a `Result.Result` JSON shape the ingester's per-type handler doesn't unmarshal. Real bug; needs investigation. Last time this risk surfaced was the `config.update` → `log.tail` extension in slice 3.

3. **Unknown device** — an ACK from a `device_id` that no longer exists in the `devices` table (decommissioned mid-flight). Benign; drain.

4. **Decode failure on the envelope itself** — IoT Rule SQL output is malformed somehow (topic(2) returned empty? message body not JSON?). Real bug.

## What to check first

1. **Pull the DLQ'd message(s):**
   ```bash
   aws sqs receive-message \
     --queue-url https://sqs.us-east-1.amazonaws.com/523612763411/uknomi-cp-cmd-result-dlq \
     --max-number-of-messages 10 --visibility-timeout 0 \
     --message-attribute-names All --attribute-names All
   ```
   Don't delete yet — you want to inspect before draining. Look at:
   - `correlation_id` + `type` + `success` in the envelope → which cmd type went wrong?
   - `device_id` → does the device still exist in `devices`?

2. **Pair with the cp-ingest log:**
   ```bash
   aws logs tail /uknomi/cp-ingest --since 30m \
     --filter-pattern '"cmd-result"' --format short
   ```
   The handler logs `level=ERROR` on writer errors and `level=INFO` on success — find the failure adjacent to the DLQ'd correlation_id.

3. **Cross-check the originating request row:**
   ```sql
   -- For log.tail
   SELECT * FROM device_log_tails WHERE correlation_id = '<corr_id>';
   -- For config.update
   SELECT service_config_last_applied_at, service_config_last_applied_corr_id
     FROM devices WHERE id = '<device_id>';
   ```
   If the row is missing (log.tail) or `last_applied` is older than the DLQ'd ACK's timestamp (config.update), the "stale request" path is the cause.

## Drain procedure

When the cause is benign (stale row, decommissioned device):

```bash
# Repeat for each DLQ'd message
aws sqs delete-message \
  --queue-url https://sqs.us-east-1.amazonaws.com/523612763411/uknomi-cp-cmd-result-dlq \
  --receipt-handle '<receipt-from-receive-message>'
```

The alarm transitions back to OK on the next 60s window once the queue is empty.

## When to escalate

- DLQ refills within minutes of draining → real bug (schema drift, decode failure). Snapshot the messages first, then dig.
- Same `correlation_id` re-appears in the DLQ after delete → consumer's poison-handling is busted; check `internal/cp/sqsconsumer/`.
- DLQ has hundreds of messages → systemic issue (cp-ingest pod down, an entire deploy broken). Check ECS service health first; the symptom is downstream.

## Related

- ADR-018: Fargate-not-Lambda for ingest; DLQ is the consumer's poison path.
- ADR-028: cmd channel is unsigned in Phase 2; an unexpected `Type` in the envelope (not in the per-type switch) is silently ignored, NOT DLQ'd, so this alarm shouldn't fire on Phase 3 cmd types being introduced.
- `uknomi-cp-service-status-dlq` runbook: identical shape; this runbook applies.
