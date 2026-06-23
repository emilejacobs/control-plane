# Offline-reason tracking — reboot detection via boot-time + shutdown cause

In-tree PRD. Implementation slices are GitHub issues under `ready-for-agent`
that reference this file.

Approved 2026-06-23. Chosen approach: "Tier 2" — the device self-reports system
boot time + the macOS previous-shutdown cause on the heartbeat it already
publishes, and CP infers the offline reason from boot-time changes. This was
chosen over capturing the AWS IoT `disconnectReason` (Tier 0) because the
reboot-vs-not-a-reboot split (with reboot causes) is the signal actually wanted;
a changed boot time = reboot, unchanged = network/MQTT blip. Tier 0 remains a
cheap future add-on for sub-splitting the network bucket.

## Implementation slices

Filed on GitHub Issues under `ready-for-agent`, in dependency order:

- **#157** — agent reports boot-time + shutdown cause; cp-ingest persists + detects reboots (`device_reboots` table). The data pipeline. No blockers. *Requires an agent release + roll to start flowing.*
- **#158** — label recovered offlines (reboot vs network blip) + enrich the notification. Blocked by #157.
- **#159** — device page: last boot / shutdown cause + reboot history. Blocked by #157.

## Problem Statement

Devices flap offline→online frequently (≈25 events / 72h across the fleet),
and operators can't tell *why* a device went offline. Some are momentary
network/MQTT blips; some are real reboots (e.g. the USB-audio watchdog reboots a
Mac when the capture device becomes unavailable — the only thing that recovers
it). Today every offline looks identical: the fleet-notification reconciler fires
an OFFLINE + recovered pair with no cause, so there's no way to distinguish noise
from a recurring hardware/reboot problem worth fixing.

## Solution

Have the agent report two cheap, static-per-boot facts on its existing
heartbeat — the **system boot time** and the **previous shutdown cause** — and
have CP use them to classify offline events. When a device reconnects with a
*changed* boot time, it rebooted (and the shutdown cause says why: clean restart
/ power loss / forced / thermal / panic); an *unchanged* boot time means it was a
network/MQTT blip, not a reboot. CP keeps a per-device reboot history for the
"is this device rebooting too often?" investigation, surfaces last-boot / cause +
reboot history on the device page, and labels the recovered notification with the
reason.

## User Stories

1. As a fleet operator, I want to know whether a device's offline was a reboot or
   just a network blip, so that I can tell noise from a real problem.
2. As a fleet operator, I want the reboot cause (clean restart / power loss /
   forced / thermal / panic), so that I know whether it was intentional, a power
   issue, or a fault.
3. As a fleet operator, I want a per-device reboot history (when + why), so that I
   can spot a device that reboots too often and investigate.
4. As a fleet operator, I want the recovered notification to say the reason —
   e.g. "recovered · reboot: clean restart" vs "network blip" — so that I learn
   the cause without opening the dashboard.
5. As a fleet operator investigating the USB-audio watchdog, I want to correlate a
   reboot with the `usb_audio` probe going red→green across the offline, so that I
   can confirm that fault is the cause.
6. As a fleet operator, I want last-boot time and last-shutdown cause on the
   device page, so that a device's recent restart state is visible at a glance.
7. As a platform owner, I want this to add negligible device overhead, so that
   tracking the reason doesn't degrade the edge fleet.
8. As a platform owner, I want unknown/unmapped shutdown codes passed through
   verbatim, so that we never lose information we haven't classified yet.
9. As a developer, I want the boot-info read + shutdown-cause mapping isolated in
   one testable module, so that the brittle log-parsing is covered.
10. As a developer, I want reboot detection to be a pure boot-time comparison, so
    that "is this a new boot?" is testable without a device.

## Implementation Decisions

- **Agent `bootinfo` reader (deep module).** Read once at agent start, cached:
  - System boot time via `unix.SysctlTimeval("kern.boottime")` (no subprocess;
    `golang.org/x/sys` is already a dependency).
  - Previous shutdown cause via the unified log
    (`log show … "Previous shutdown cause"`), parsed to an integer code + a mapped
    label. Mapping (refined against real device logs during impl): `5` → clean
    restart, `3` → forced/hung, `0` → power loss, negative codes → thermal /
    hardware / watchdog, kernel panic → panic, anything unmapped → `unknown` with
    the raw code preserved.
  - macOS-only; on other platforms the fields are omitted (the fleet is Macs).
- **Heartbeat carries `boot_time` + `last_shutdown_cause` (+ raw code).** Emitted
  by a heartbeat collector (the existing `defaultCollectors` pattern), reusing the
  static cached values — two extra JSON fields per heartbeat, no per-tick cost.
- **cp-ingest parses + persists.** The ingest `Heartbeat` struct gains the fields.
  On each heartbeat, compare the reported `boot_time` to the device's stored
  boot_time; on a change (including first-ever), record a **reboot event** in a new
  `device_reboots` history table (device_id, boot_time, shutdown_cause, code,
  detected_at) and update the device's last boot_time + cause. First contact for a
  device is recorded but not alerted as a "reboot."
- **Offline reason on recovery.** When the notification reconciler resolves a
  device-offline alert, it determines the reason: if a reboot event was recorded
  during the offline window (boot_time changed between going offline and
  recovering) → `reboot: <cause>`; otherwise → `network blip`. The reason rides in
  the resolved `AlertEvent` and is rendered into the recovered digest.
- **Device-page surfacing.** Show last boot (relative) + last shutdown cause, and
  a short recent-reboot list (time + cause). Read-only.
- **No new always-on probing.** Everything is derived from one boot-time read +
  one log read at agent start, plus data CP already has. The `usb_audio`
  correlation (Story 5) uses the existing health-probe data — no new probe.
- **Agent release.** This requires a new agent version + fleet roll; reasons only
  flow once the new agent is deployed. No retroactive classification.

## Testing Decisions

A good test asserts external behaviour — the cause label produced from sample log
text, the reboot row produced from a boot-time change — not the implementation.

- **`bootinfo` parse/mapping (agent) — unit-tested.** Given representative
  `log show` output lines for each known code (5/3/0/negatives/panic) and an
  unmapped code, assert the parsed code + mapped label, including the
  pass-through-unknown case. The `sysctl`/`log` calls sit behind a seam so the
  test drives the parser with fixture strings (no device).
- **Reboot detection + offline-reason (cp-ingest) — integration-tested**
  (testcontainers Postgres, mirroring the existing ingest/registry tests):
  - a heartbeat with a new `boot_time` inserts one `device_reboots` row with the
    cause; a repeat heartbeat with the same `boot_time` inserts none;
  - across a simulated offline→online window, a boot-time change yields a
    `reboot: <cause>` reason and an unchanged boot-time yields `network blip`.
  Prior art: the existing heartbeat/lifecycle ingester tests and `FleetAlerts`/
  `FleetCameras` registry tests.

## Out of Scope

- AWS IoT `disconnectReason` capture (Tier 0) — deferred; a cheap future add-on to
  sub-split the network-blip bucket (keepalive vs takeover) if a device shows many
  non-reboot flaps.
- The offline-notification debounce discussed separately — orthogonal; can layer
  on later (this PRD explains reasons, debounce suppresses noise).
- Modifying the colleague's USB-audio watchdog service — the `usb_audio` probe +
  reboot correlation infers its action without touching it.
- Linux/Radxa boot-info — the fleet is consolidating on Macs; non-Darwin omits the
  fields.
- Health-probe transition history (`status_changed_at` on probes) — not required;
  the existing probe data + alert-state timestamps suffice for the USB-audio
  correlation.

## Further Notes

- The agent already sends `uptime_seconds` (agent-process uptime), but cp-ingest
  drops it and it resets on agent restart too — so it's not a reliable reboot
  signal. System `boot_time` replaces it as the clean discriminator; the existing
  field can stay or go.
- Reboot-vs-blip is inferred entirely from boot-time deltas, so it's robust even
  when CP misses the exact disconnect event (e.g. during a cp-ingest deploy).
