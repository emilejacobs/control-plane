# Phase 0 macOS smoke — results

**Date:** 2026-05-20 (evening, CDT)
**Environment:** Personal Mac Mini (Apple Silicon, macOS 26.2 / Darwin 25.2.0, on a home network — NOT a client site).
**Device id:** `dev-mac-mini-emile`
**Cert:** AWS IoT Core managed (`aws iot create-keys-and-certificate`), region `us-east-1`, account `523612763411`.

This file is the captured-results artefact called out in [Issue 07's acceptance criteria](issues/07-field-deployment-mac.md). It records what the personal-Mac spike actually covered; the full client-site HITL acceptance is **not** closed by this run — see "Deferred / not covered" below.

## What was tested

| Issue | Result |
| --- | --- |
| 03 service.status (running) | ✅ `{"name":"com.uknomi.test-target","state":"running"}` |
| 03 service.status (not found) | ✅ `Error.Code="service.not_found"` for `com.example.no-such-service` |
| 04 service.restart | ✅ Success with `started_at`/`finished_at`. PID of test target actually changed (34106 → 34141), confirming `launchctl kickstart -k` landed. |
| 05 telemetry | ✅ 2 publishes seen on `devices/dev-mac-mini-emile/telemetry` at the 10s interval. Payload contained `device_id`, `version`, `os`, `uptime_seconds`, `last_command_at` (populated after earlier commands), `correlation_id` (fresh per tick). |
| 06 LaunchDaemon install | ✅ Plist installed at `/Library/LaunchDaemons/com.uknomi.agent.plist`, `launchctl bootstrap system` succeeded, agent process running. |
| 06 kill -9 recovery | ✅ `kill -9 <pid>` followed by `launchctl print` confirmed PID change within the 5s `ThrottleInterval`. Heartbeat after relaunch succeeded. |
| 07 reboot persistence | ✅ `sudo shutdown -r now` → SSH back → agent already running (PID 2110, post-boot uptime 45s when heartbeat was sent). |

## Round-trip latency

Single heartbeat round-trip after install: < 2s (subjectively — not formally measured across 10 trials). The 10-trial median measurement called out in Issue 07's acceptance criteria was **not** executed.

## Notable findings (worth recording)

- **`agent-cli` cannot connect with the policy-as-shipped.** The IoT Core provisioning runbook ([`docs/runbooks/phase-0-iot-core-provisioning.md`](../../docs/runbooks/phase-0-iot-core-provisioning.md)) ships a policy that scopes `iot:Connect` to `client/${iot:Connection.Thing.ThingName}` and `iot:Subscribe` to `topicfilter/devices/${iot:Connection.Thing.ThingName}/cmd` only. Two problems:
  1. AWS IoT only substitutes `${iot:Connection.Thing.ThingName}` when the connecting `client_id` equals the thing name. `agent-cli` generates a random `agent-cli-xxx` client_id, so the variable resolves to empty and Subscribe is denied.
  2. The Subscribe rule does not include `cmd-result`, which is exactly what the CLI needs to subscribe to.

  Mitigation in this session: broadened `iot:Connect` to `client/*` and added `cmd-result` / `telemetry` topic filters to Subscribe. The cert is still the principal, so pub/sub remain thing-scoped via `${...}` in the topic ARNs. **Tracked in [Issue 10](issues/10-agent-cli-iot-policy-fix.md).**

- **macOS clears `/tmp` on reboot.** The install/uninstall scripts I staged at `/tmp/uknomi-{install,uninstall}.sh` survived `kill -9` but not the reboot — had to re-`scp` the uninstall script. For Phase 1, install scripts should be staged somewhere persistent (the Mosyle install path, or `mac-mini-rollout/modules/`).

- **Local PID 34003 → 34417 timing.** Initial confusion when uptime in the post-kill heartbeat looked too large. Cross-checked with `ps -p <pid> -o lstart=` and the math reconciled: heartbeat was sent ~65s before the cross-check, and `uptime_seconds(heartbeat) + 65 = process_etime(ps)` to the second. The agent's `time.Since(startTime)` is correct; the diagnosis was operator error.

## Deferred / not covered

Issue 07's acceptance criteria that this run did **not** satisfy, and why:

| Criterion | Status | Why |
| --- | --- | --- |
| Client site selected, operator approval recorded | Skipped | Personal Mac standing in for client deployment. |
| Target service selected and approved | Skipped | Used a throwaway `com.uknomi.test-target` LaunchDaemon (sleep 3600) so the restart was risk-free. |
| 10 trials of heartbeat, median < 2s | Not measured | Single trial only. |
| Network-blip reconnect (disable Wi-Fi ~60s) | Not tested | Skipped to avoid breaking the SSH session driving the smoke. |
| Telemetry observed ≥ 10 minutes | Partial | Only ~25 seconds of telemetry observation (2 publishes). |

These are real HITL items and remain pending. If the personal-Mac result is treated as sufficient evidence for Phase 0 closure, that's a deliberate scope decision; the gaps above should be re-opened only if a Phase 1 deployment surfaces issues that map back to them.

## Teardown

All resources removed at end of session:
- AWS: cert detached/deactivated/deleted, thing `dev-mac-mini-emile` deleted, `UknomiAgentPolicy` (versions 1–3) deleted. `aws iot list-things` and `list-policies` both empty.
- Mac Mini: `/Library/LaunchDaemons/com.uknomi.{agent,test-target}.plist`, `/etc/uknomi/`, `/usr/local/bin/uknomi-agent`, `/var/log/uknomi-agent.log` all removed.
- Local: workspace `~/.uknomi/dev-mac-mini-emile/` removed.
