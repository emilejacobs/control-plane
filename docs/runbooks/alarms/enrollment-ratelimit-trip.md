# Alarm — `uknomi-cp-enrollment-ratelimit-trip`

**Fires when**: any `ratelimit.trip` log line lands in the cp-api log group over a 5-minute window — meaning at least one IP exceeded the per-IP enrollment rate (20 req/hour, ADR-017).

**Why it matters**: a legitimate site rollout enrolls ~10 devices over an afternoon — well under the limit. A trip is either a misconfigured install script in a loop or, in the worst case, someone with a leaked bootstrap key probing enrollment at scale. The alarm pages immediately so the leak window stays short.

## What to check first

1. **Which IP tripped, and how often.** The aggregate metric does not carry the IP; the Insights query does.
   ```bash
   aws logs start-query --log-group-name /uknomi-cp/cp-api \
     --start-time $(date -u -v-30M +%s) --end-time $(date -u +%s) \
     --query-string 'fields @timestamp, source_ip
                     | filter msg = "ratelimit.trip"
                     | stats count(*) by source_ip
                     | sort by count(*) desc'
   ```

2. **Is the IP a legitimate site?** Cross-reference against the ops record of WAN egress IPs per site. A site rollout day with a buggy install script (e.g., a loop that retries enrollment on every restart) is the most common cause.

3. **Are the enrollments succeeding before the trip?** Pull the `audit.enrollment` rows for that IP:
   ```bash
   aws logs start-query --log-group-name /uknomi-cp/cp-api \
     --start-time $(date -u -v-30M +%s) --end-time $(date -u +%s) \
     --query-string 'fields @timestamp, msg, outcome, reason, hostname, hardware_uuid
                     | filter source_ip = "<the IP>"
                     | filter msg like /audit.enrollment/
                     | sort @timestamp desc'
   ```
   - All `outcome=success` with the same `hardware_uuid` → idempotency replay; the install script is hitting `/enrollments` in a tight loop. The DB is fine (ADR-012 dedup), but the script is broken.
   - Mixed `success`/`failure` with shifting `hardware_uuid` → someone is enrolling fake devices. **Treat as a credential leak**: invalidate the bootstrap key (rotate the Secrets Manager value; the cp-api `bootstrap.Verifier` picks it up on next mismatch).

## Escalation

- For a benign install-script loop: contact the site operator running the rollout, kill the loop, and let the limit window expire. No code change needed.
- For a suspected bootstrap-key leak: **rotate the key immediately** per the ADR-017 rotation runbook (TODO: link when written). Then audit `audit.enrollment` rows from the suspect IP since the last known-good key value to identify which devices need their certs revoked.
