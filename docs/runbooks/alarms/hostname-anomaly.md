# Alarm — `uknomi-cp-hostname-anomaly`

**Fires when**: any `audit.enrollment.anomaly` line lands in the cp-api log group over a 5-minute window — a device enrolled with a hostname that did not match the project convention regex (`^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$`, ADR-017).

**Why it matters**: the convention is a sanity check, not an allowlist — the enrollment still completed and the device is now in the registry. The alarm exists so a typo in the install command (e.g. `mac-mini-acme-1` instead of `mac-mini-acme-01`) gets caught while the install script is still on someone's terminal, rather than after the device has been racked and the operator has gone home.

## What to check first

1. **Which hostname tripped it.**
   ```bash
   aws logs start-query --log-group-name /uknomi-cp/cp-api \
     --start-time $(date -u -v-15M +%s) --end-time $(date -u +%s) \
     --query-string 'fields @timestamp, hostname, hardware_uuid, source_ip, device_id
                     | filter msg = "audit.enrollment.anomaly"
                     | sort @timestamp desc'
   ```
   The hostname will almost always reveal the typo (missing leading zero, wrong site abbreviation, double dash). The `source_ip` tells you which site the install happened from.

2. **Confirm the device was actually meant to enroll.** Cross-reference `hardware_uuid` against the inventory the rollout coordinator maintains. If the hardware isn't in the inventory, treat this as a near-miss for an unauthorized enrollment (the bootstrap key is shared, so this can happen).

3. **Verify the device is actually online.** The convention miss is a naming issue, not a connectivity issue, but check that the agent is sending heartbeats:
   ```bash
   aws iot list-thing-principals --thing-name <hostname>
   ```

## Escalation

- For a hostname typo: reach out to the site operator to either re-enroll the device with the correct name (delete the wrong row + revoke the cert, run the install script again) or update the registry's `devices.hostname` column directly if the device is already deployed and renaming it in-place is more expensive than fixing the DB row. Document which path you took in the rollout's wave runbook.
- For an unrecognised `hardware_uuid`: revoke the cert immediately (`aws iot update-certificate --certificate-id <id> --new-status REVOKED`), delete the row, and rotate the bootstrap key as in `enrollment-ratelimit-trip.md`'s escalation if there's any chance it leaked.
- If multiple anomalies fire in quick succession with progressively-different-looking hostnames: someone is probing the registry. Treat as a confirmed bootstrap-key leak.
