# Runbook — Decommission a device from the Control Plane

Removing a device (e.g. a Mac Mini being pulled from a store) is **three layers**, and it's easy to stop after the first and leave orphans. Do all three.

> Phase 1 has no automated CP revocation/decommission flow (that pairs with the Phase 3 cert-rotation work, ADR-013). Until then this is a manual runbook.

Worked example below uses a real decommission: `uKnomis-Mac-mini-2` (`100.90.4.34`), `device_id 7bc9b66e-bea3-446b-9407-e3731de293c7`.

---

## Layer 1 — Local agent (on the device, over SSH)

**Capture the `device_id` first** — you need it for layers 2 & 3, and it's about to be deleted:
```bash
ssh -t uknomi@<device> 'sudo python3 -c "import json;print(json.load(open(\"/var/uknomi/agent-config.json\")).get(\"device_id\",\"\"))"'
```

Then remove the agent. If the device was provisioned with the full package, `mac-mini-rollout/uninstall-cp-agent.sh` does this; for devices onboarded with the standalone `install-cp-agent-only.sh` (which doesn't carry `lib/`), use the inline equivalent:
```bash
ssh -t uknomi@<device> '
  sudo launchctl unload /Library/LaunchDaemons/com.uknomi.agent.plist 2>/dev/null || true
  sudo rm -f /Library/LaunchDaemons/com.uknomi.agent.plist
  sudo rm -f /usr/local/bin/uknomi-agent
  sudo rm -rf /var/uknomi          # cert, key, config
'
```
Verify: `sudo launchctl print system/com.uknomi.agent` → not found; `/var/uknomi` gone.

## Layer 2 — AWS IoT identity (thing + mTLS cert)

The local removal does **not** revoke the device's identity — the IoT thing + cert remain and could still authenticate. Delete them. **Detach the shared `UknomiAgentPolicy`, never delete it** (every device's cert uses it).

```bash
THING=<device_id>
CERTARN=$(aws iot list-thing-principals --thing-name "$THING" --query 'principals[0]' --output text)
CERTID="${CERTARN##*/}"

# Safety: confirm this cert isn't shared with another thing
aws iot list-principal-things --principal "$CERTARN"

aws iot detach-policy --policy-name UknomiAgentPolicy --target "$CERTARN"   # policy stays; just this cert's link
aws iot detach-thing-principal --thing-name "$THING" --principal "$CERTARN"
aws iot update-certificate --certificate-id "$CERTID" --new-status INACTIVE  # required before delete
aws iot delete-certificate --certificate-id "$CERTID"
aws iot delete-thing --thing-name "$THING"
```
Verify both 404:
```bash
aws iot describe-thing --thing-name "$THING"            # ResourceNotFoundException
aws iot describe-certificate --certificate-id "$CERTID" # does not exist
aws iot get-policy --policy-name UknomiAgentPolicy      # still present (sanity — fleet unaffected)
```

## Layer 3 — CP device record (Postgres `devices` table)

Layers 1–2 leave the device's row in CP's `devices` table. Remove it with the
staff-only decommission endpoint:

```
DELETE /devices/{device_id}
```

It deletes the device row (child rows — services, health probes, cameras, log
tails, network scans — cascade), is audited (`audit.device_decommission`), and
returns `204` on success / `404` if the device is already gone. Do this **after**
layers 1–2 so the record isn't removed while the device could still reconnect.

> Earlier deployments had no such endpoint (the row had to be left as a
> permanently-offline record or deleted directly in RDS); that gap is closed.
> Cert revocation remains layer 2's manual step until the Phase 3
> rotation/revocation work (ADR-013).

---

## Quick checklist

- [ ] Captured `device_id`
- [ ] Layer 1: daemon unloaded, plist + binary + `/var/uknomi` removed
- [ ] Layer 2: cert detached from policy + thing, deactivated, deleted; thing deleted; both verified 404; shared policy intact
- [ ] Layer 3: `DELETE /devices/{id}` (staff) — CP record removed (204)
