# Camera observability — import existing cameras + per-camera offline status & notifications

Two related pieces of work that make the CP aware of every camera and alert when one goes dark:

- **(A) Import** the cameras already configured in each Mac's Edge UI into CP — a **one-time
  operator script**, since all future cameras are managed in CP from install/commission onward.
- **(B) Per-camera offline status** — the agent probes each camera's reachability, reports status,
  CP surfaces a badge per camera, and an offline camera flows into the existing email + Teams
  notification digest as a new `camera_offline` alert kind.

## Design source of truth

Builds on settled surfaces — don't re-derive:
- **Cameras inventory (#2, ADR-030 §1)** — `device_cameras` (device_id, camera_id, label, rtsp_url,
  is_lpr), CRUD API `GET/POST/PUT/DELETE /devices/{id}/cameras`, and the CP→agent `cameras.update`
  push. This is **push-only** today; CP never learns the device's locally-configured cameras — hence
  the import (A).
- **Fleet notifications v1 (ADR-039, #94)** — the cp-ingest `NotificationReconciler` diffs a
  system-actor `FleetUnhealthy` snapshot (offline / service-stopped / probe-red) against `alert_state`
  and fan-outs a fire+resolve, per-tick digest to SES email + a Teams Workflows webhook. Adding a
  fourth unhealthy kind plugs straight in.
- **Telemetry report pipelines** — service-status and health-probes each ride agent→IoT→SQS→cp-ingest
  ingester (one queue + IoT rule + ingester apiece). Camera status mirrors that shape.
- **Agent snapshot** — the agent already shells `ffmpeg` against an RTSP URL with a TCP transport +
  timeout (the on-demand snapshot, #8); the reachability probe reuses that capability.

## Problem Statement

Two gaps. First, the cameras at every site were configured **locally** in each Mac's Edge UI and the
CP has no record of them — so the CP's Cameras panel is empty for the existing fleet even though the
cameras exist. Second, **a camera can go offline and nobody notices** until someone happens to open a
feed and sees "Host is down" — there's no proactive signal. The operator wants CP to know about every
camera and to be told, in email + Teams, the moment a camera drops (and when it recovers), the same
way device/service/probe alerts already work.

## Solution

**(A) One-time import.** An operator-run script loops over the Mac fleet (Tailscale IPs), reads each
device's CP `device_id` and its Edge-UI camera list (label + RTSP URL), and upserts those cameras
into CP through the existing cameras API. After this runs once, CP holds the full inventory; all
future cameras are added in CP directly (install/commission), so no ongoing agent→CP import is built.

**(B) Per-camera offline status + notifications.** The agent periodically probes each of its cameras'
RTSP reachability (reusing the ffmpeg/RTSP path, debounced so a single transient miss doesn't flap),
and reports per-camera status on a new camera-status telemetry channel. cp-ingest records the status
on the camera row (`status`, `last_checked_at`, `status_changed_at`). The device-page Cameras panel
shows an online/offline badge per camera. A camera in `offline` status becomes a new `camera_offline`
entry in the fleet-unhealthy snapshot, so the existing reconciler fires it into the email + Teams
digest — fire when it drops, recovery when it returns — with a camera-specific line (camera label ·
device name · site · CP link), bounded by the same per-tick cap as every other alert.

## User Stories

1. As an operator, I want the cameras already set up in each Mac's Edge UI to appear in CP, so that the CP Cameras panel reflects reality for the existing fleet without re-entering everything by hand.
2. As an operator, I want the import to carry each camera's label and RTSP URL, so that the CP records match what's actually deployed.
3. As an operator, I want the import to be idempotent, so that re-running it doesn't create duplicate camera rows.
4. As an operator, I want the import to map each Mac to the right CP device automatically, so that cameras land on the correct device record.
5. As an operator, I want a dry-run mode on the import, so that I can preview what will be inserted before writing anything.
6. As an operator, I want the import to skip or clearly report a device it can't reach or can't read, so that one bad device doesn't abort the whole run.
7. As an operator, I want a notification when one of my cameras goes offline, so that I find out immediately instead of when someone opens the feed.
8. As an operator, I want that camera-offline alert in both email and MS Teams, so that it reaches me wherever device/service alerts already do.
9. As an operator, I want a recovery notice when a camera comes back online, so that the alert closes the loop like the others.
10. As an operator, I want the camera alert to name the camera (its label), the device, and the site, so that I know exactly which camera at which store to check.
11. As an operator, I want the camera name in the alert to link to its device's CP page, so that I can jump straight to it (matching the device-name links already in the digest).
12. As an operator, I want a brief transient blip not to fire an alert, so that a momentary network hiccup doesn't spam me.
13. As an operator, I want simultaneous camera outages (e.g. a site's switch dies) coalesced into the one per-tick digest, so that I get one message, not one per camera.
14. As an operator, I want each camera on the device page to show an online/offline badge and when it was last checked, so that I can see status at a glance without taking a snapshot.
15. As an operator, I want a camera whose status is not yet known to read "unknown" rather than a false "online/offline", so that a freshly-imported or just-added camera isn't misreported.
16. As an operator, I want camera status to keep working across a cp-ingest restart, so that a deploy doesn't re-fire every currently-offline camera or lose one that dropped during the restart.
17. As an operator, I want notifications to remain controllable by the existing enable switch, so that pausing notifications also pauses camera alerts.
18. As an engineer, I want the reachability probe to be cheap, so that probing every camera on a cadence doesn't load the device or the cameras.
19. As an engineer, I want the camera-offline signal to reuse the existing reconciler + alert_state + digest, so that no parallel notification path is introduced.
20. As an engineer, I want per-camera status modeled on the camera row (not health-probes), so that the panel and the alert can speak in camera terms (label, id) rather than probe terms.
21. As an engineer, I want the import to be a standalone one-shot, so that no agent code or new wire protocol is added for a migration that runs once.

## Implementation Decisions

### (A) Import is a one-time operator script, not an agent/CP feature
A standalone script loops the fleet's Tailscale IPs, and per device: reads the CP `device_id` from the
device's agent config, reads the Edge-UI camera list (label + RTSP URL) from its local config, and
upserts each camera into CP via the existing `POST /devices/{id}/cameras` (authenticated as a staff
operator). Idempotent (existing `(device_id, camera_id)`/label match is skipped or updated, not
duplicated). Supports a **dry-run** that prints the planned inserts. Per-device failures are logged
and skipped, never fatal to the run. **No agent changes, no new wire protocol** — future cameras are
managed in CP directly.

**Sources (confirmed):** the Edge UI persists cameras at `/usr/local/etc/uknomi/cameras.json` — a JSON
array of `{label, rtsp_url, lpr}` — which maps directly to CP's `{label, rtsp_url, is_lpr}` (camera_id
is server-assigned by CP on POST). The CP `device_id` is read from the device's agent config
(`/var/uknomi/agent-config.json`). So per device the script: read device_id → read cameras.json → POST
each `{label, rtsp_url, is_lpr}` to `/devices/{device_id}/cameras`.

### (B) Per-camera status lives on the camera row (dedicated surface, not health-probes)
A migration adds to `device_cameras`: `status` (text: `online` | `offline` | `unknown`, default
`unknown`), `last_checked_at` (nullable), and `status_changed_at` (nullable). Registry methods update
status from an ingested report and expose it on the camera read. This keeps status in camera terms for
both the panel and the alert — explicitly **not** modeled as a `device_health_probes` row.

### Agent: camera reachability prober
A new agent component probes each configured camera on a cadence (minutes-scale, far slower than a
live view) using a **lightweight RTSP reachability check** (prefer a fast `ffprobe`/RTSP-OPTIONS/TCP
dial to the stream host over a full JPEG capture, to keep probing cheap). It **debounces**: a camera
flips to `offline` only after N consecutive failures and back to `online` on the first success, so a
single transient miss doesn't alert. The agent reports each camera's current status (per camera id)
on a new camera-status telemetry channel.

### Camera-status telemetry channel (mirrors service-status)
A new agent→CP report rides the established pattern: agent publishes to a camera-status MQTT topic →
IoT rule → a dedicated SQS queue → a cp-ingest camera-status ingester that updates the camera row's
`status` / `last_checked_at` / `status_changed_at`. The ingester is opt-in by env (queue URL unset →
skipped), same posture as the service-status / health-probes consumers, so the code can deploy before
the queue is provisioned.

### Notifications: a fourth `camera_offline` unhealthy kind
`FleetUnhealthy` gains a `camera_offline` kind — one entry per camera whose `status = 'offline'`,
carrying the device hostname + site (already joined) plus the **camera label** as the alert subject.
The reconciler, `alert_state` dedupe (keyed `(kind, device_id, subject)` — subject = camera id, so
each camera is its own alert), fire+resolve, per-tick digest, cap, and at-least-once behavior all
apply unchanged. Rendering adds a `CAMERA OFFLINE` label; the digest line reads
`CAMERA OFFLINE · <device> · <camera label> (<site>)` with the device-name CP link already in place.
The enable switch and channel config are unchanged.

### Dashboard: per-camera status badge
The device-page Cameras panel shows a status badge per camera (online / offline / unknown) and the
last-checked time, read from the camera record. No new page — an addition to the existing panel.

## Testing Decisions

A good test asserts **observable behavior at module boundaries** — given probe outcomes, what status
is reported; given a status snapshot, what the digest contains — never internal structure.

- **Agent prober debounce** (deep module, the core): table-driven unit tests against a fake
  reachability checker — N consecutive failures flip to offline; one success flips back; a single
  miss inside the window does not flip; status is reported per camera id. Prior art: the agent's
  existing probe/telemetry tests.
- **Camera-status ingester**: unit test that an ingested per-camera report updates status +
  timestamps; an unknown/decommissioned device id is handled like the other ingesters. Prior art:
  service-status / health-probe ingester tests.
- **`FleetUnhealthy` + reconciler `camera_offline`**: the camera-offline kind appears for an offline
  camera and clears on recovery; it fires once, recovers once, coalesces, and respects the cap —
  reusing the reconciler's fake store + fake notifier. Prior art: the existing reconciler diff tests.
- **Digest rendering**: the camera line shows the camera label + device name as a CP link, in both the
  Teams adaptive card and the email. Prior art: the existing render tests.
- **Web badge**: the Cameras panel renders the per-camera status badge + last-checked, including the
  `unknown` state. Prior art: the existing device-page panel tests.
- **Registry status methods**: reviewed SQL (migration + update/read), consistent with the repo's
  convention of not DB-testing registry methods directly.
- The **import script** is an operator one-shot — verified by its dry-run + a manual run against a
  device, not unit-tested.

## Out of Scope

- **Ongoing agent→CP camera import / two-way sync** — import is one-time; future cameras are managed in
  CP. No `cameras.report` agent command or wire protocol.
- **Live preview / streaming health** — status is reachability of the RTSP source, not frame quality,
  bitrate, or stream-content checks.
- **Per-camera notification routing / muting** — camera alerts ride the single recipient list + Teams
  webhook and the global enable switch, like every other alert kind.
- **A camera-status history / uptime view** — only current status + last-checked are surfaced.
- **Yellow/degraded camera states** — v1 is binary online/offline (+ unknown before first probe).
- **Probing cameras the CP doesn't know about** — only cameras in `device_cameras` are probed, which is
  why the import (A) precedes useful status for the existing fleet.

## Further Notes

- **Edge-UI storage location confirmed** — `/usr/local/etc/uknomi/cameras.json` (array of
  `{label, rtsp_url, lpr}`); device_id from `/var/uknomi/agent-config.json`. No remaining import
  prerequisite.
- **First-probe / freshly-imported cameras** read `unknown` until the first report, so the import
  doesn't instantly fire alerts for cameras whose status isn't established yet.
- **Cheap probing matters** — every ALPR/site camera probed on a cadence across ~25 Macs; favor a
  connect-level reachability check over full frame capture, and a cadence in minutes.
- **The camera link** points at the device page (where the Cameras panel lives), reusing the
  dashboard-base-URL the notifier already has.
- **Slicing:** natural tracer-bullet order — (1) `device_cameras` status columns + registry; (2) agent
  prober + camera-status telemetry channel + cp-ingest ingester; (3) `camera_offline` in FleetUnhealthy
  + reconciler/render; (4) web status badge; (5) the one-time import script. Run `to-issues` to cut them.
