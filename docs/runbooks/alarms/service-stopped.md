# Alarm — `uknomi-cp-service-stopped`

**Fires when**: ≥1 `service-status.stopped` log line landed in cp-ingest across 3 consecutive 5-minute reporting windows (i.e. the same — or any — allow-listed service has been observed stopped for ~15 minutes on at least one device).

**Why it matters**: an allow-listed service is the agent's declaration that "this thing matters on this device" — the Edge UI (`com.uknomi.edge-ui`), nginx, etc. A persistently-stopped allow-listed service is real degradation that operators previously had to SSH in to spot. Phase 2's whole point is closing that gap.

**Known false-positive: operator-initiated stops.** Slice 1's agent backend (launchctl/systemctl) reports the same `stopped` state for "I crashed" and "the operator deliberately stopped me." The first follow-on slice that distinguishes the two is Phase 3 (where service-control commands need to read exit codes anyway). Until then, treat the alarm as "investigate" not "page".

## What to check first

1. **Which device, which service?**
   ```bash
   aws logs tail /uknomi/cp-ingest --since 30m \
     --filter-pattern '"service-status.stopped"' --format short
   ```
   Each line carries `device_id`, `service`, `state_since`, `correlation_id`. Pick the most recent or most-repeated combination.

2. **Is the device actually online?**
   ```bash
   # Replace <device_id> with the value from step 1.
   curl -sS "https://api.control.uknomi.com/devices/<device_id>" \
     -H "Authorization: Bearer $(./scripts/mint-token.sh)" | jq '.is_online, .last_seen_ago_seconds'
   ```
   - Offline → the agent stopped reporting; the service rows are stale. Investigate the device's connectivity first; the stopped-state report may be a snapshot from before it went offline. The alarm OKs naturally once the device comes back and reports the service running.
   - Online → the agent is alive and actively reporting the service as stopped. Proceed to step 3.

3. **Cross-reference with the device's `/devices/{id}` Services panel** (dashboard at `https://control.uknomi.com/devices/<device_id>`). Confirms what cp-ingest stored matches what the agent reported, and shows `state_since` so you can tell whether this is a recent stop or one that's been there for hours.

## Investigating on the device

If you have Tailscale access:

```bash
tailscale ssh <hostname>
# Mac:
launchctl list <service-name>
launchctl print system/<service-name>     # the full plist + last exit status
# Linux:
systemctl status <service-name>
journalctl -u <service-name> --since '30 min ago'
```

What to look for:
- **Last exit code ≠ 0** → real failure. Check journal/syslog for the crash reason.
- **Last exit code 0 + no recent crash** → most likely operator-initiated; confirm with whoever was on the device.
- **Service not loaded at all** → the allow-list may name a service that doesn't exist on this device (e.g. an old Linux unit name on a Mac). Update the device's `service_allow_list` config and reload the agent.

## Recovery

Depends on what step 3 reveals. There is no automatic recovery — Phase 3 will add `service.restart` as a signed command; until then, restart via `launchctl kickstart -k system/<name>` (Mac) or `systemctl restart <name>` (Linux) over the SSH session.

## Tuning the alarm

The alarm threshold is "≥1 stopped report across 3 consecutive 5-min windows" = 15 minutes. If false-positive rate from operator stops becomes a problem, two levers:
- Tighten the allow-list per device so only services that should *never* be stopped trip the alarm.
- Bump `evaluation_periods` in `infra/terraform-deploy/alarms.tf` to 6 (30 min) once the team gets a feel for the noise profile.

Both are reversible decisions; pick after operating for ~2 weeks.
