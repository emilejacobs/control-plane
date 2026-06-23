# Enhanced fleet overview dashboard — gauges + camera alerts

In-tree PRD for the overview-page enhancement. Implementation slices are GitHub
issues under the `ready-for-agent` label that reference this file.

Design approved 2026-06-23 via mockups (radial-arc gauge style chosen over
speedometer/linear options; full-page mock signed off).

## Implementation slices

Filed on GitHub Issues under `ready-for-agent`, in dependency order:

- **#151** — reusable radial `Gauge` + Devices/Version gauges (existing data). No blockers.
- **#152** — Camera alerts panel + Cameras gauge (`registry.FleetCameras` + `GET /fleet/cameras`; integration-tested). Blocked by #151.
- **#153** — Services-online gauge (service online/total counts on `/fleet/alerts`). Blocked by #151.

## Problem Statement

The fleet overview page (`/overview`, also the root `/`) opens with three flat
stat tiles (Online, Cert expiring ≤30d, Agent version drift). An operator
scanning the page has to read numbers to judge fleet health — there's no
at-a-glance visual signal of what's healthy vs what needs attention. Worse, the
overview has **no camera signal at all**: camera reachability is tracked
per-device (camera observability, #112/#115) but never rolled up, so an operator
cannot tell from the dashboard that cameras are offline across the fleet — they'd
have to open devices one by one.

## Solution

Replace the flat stat tiles with a row of four **radial-arc gauges** that
status-colour themselves (green = healthy, amber = needs attention) so issues pop
visually, and add a dedicated **Camera alerts** panel that lists the offline
cameras fleet-wide with their site and how long they've been down. The existing
**Needs attention** panel (offline devices + cert-expiring-soonest) is retained
unchanged.

The four gauges: **Services online**, **Devices online**, **Cameras online**,
**Agent version conformance**. Devices-online and version-conformance derive from
the existing `GET /devices` summary; Cameras and Services need new fleet
roll-ups.

## User Stories

1. As a fleet operator, I want the overview to show health as gauges rather than
   bare numbers, so that I can spot a problem at a glance without reading every
   figure.
2. As a fleet operator, I want each gauge to turn amber when its metric crosses a
   health threshold, so that colour alone tells me where to look.
3. As a fleet operator, I want a Devices-online gauge showing online/total with
   the offline count, so that I can see fleet availability instantly.
4. As a fleet operator, I want a Cameras-online gauge showing online/total across
   the whole fleet, so that I know overall camera health without opening devices.
5. As a fleet operator, I want a Services-online gauge (ALPR / transcriber /
   raven across the fleet), so that I can see whether edge services are healthy
   in aggregate.
6. As a fleet operator, I want an Agent-version-conformance gauge, so that I can
   see how much of the fleet is on the current agent build and spot drift.
7. As a fleet operator, I want a Camera alerts panel listing every offline
   camera, so that I have a single place to see which cameras are down.
8. As a fleet operator, I want each offline camera to show its label, its site,
   and how long it has been offline, so that I can prioritise which to chase.
9. As a fleet operator, I want the offline-camera list ordered worst-first
   (longest outage at the top), so that the most urgent outage is most prominent.
10. As a fleet operator, I want the offline duration colour to escalate (amber for
    a recent drop, red for a long outage), so that severity is visible at a glance.
11. As a fleet operator, I want a count badge on the Camera alerts panel, so that
    I see the number of offline cameras immediately.
12. As a fleet operator, I want a "view all cameras" affordance, so that I can
    drill from the alert summary into the full camera inventory.
13. As a fleet operator, I want the Needs-attention panel (offline devices + cert
    expiring soonest) kept, so that I lose none of the current overview signal.
14. As a scoped operator, I want all gauges and the camera panel to respect my
    site scope, so that I only see the slice of the fleet I'm responsible for.
15. As a fleet operator, I want the camera roll-up to refresh on the same polling
    cadence as the rest of the overview, so that the dashboard stays current.
16. As a fleet operator, I want the camera panel to show a clear empty state ("all
    cameras online") when nothing is offline, so that an empty list is
    unambiguous rather than looking broken.
17. As a fleet operator, I want the gauges and camera panel to degrade gracefully
    when their data fails to load, so that one failing call doesn't blank the
    whole page.
18. As a developer, I want the radial gauge to be a single reusable component, so
    that all four gauges (and future ones) share one tested implementation.
19. As a developer, I want the fleet camera roll-up to be one query behind a
    simple store method, so that it's testable in isolation and cheap (one round
    trip, not N per-device calls).

## Implementation Decisions

- **`registry.FleetCameras(ctx)` (new deep module).** One site-scoped SQL query
  across the `cameras` table joined to devices and sites, returning a
  `FleetCameraRollup`: `total`, `online`, `offline` counts plus an `offline` list
  of `{ camera label, device id, hostname, site name, status_changed_at }`.
  Site-scoping is applied inside the store from the request's resolved
  `SiteFilter`, exactly as `FleetAlerts` does — the handler stays oblivious to
  authz. The offline list is ordered oldest-`status_changed_at`-first
  (longest-outage first). This is a single round trip; no per-device fan-out.
- **`GET /fleet/cameras` (new handler).** Lives in the existing `handlers/fleet`
  package next to `AlertsHandler`, with the same `…Store` interface seam. Returns
  `{ total, online, offline, cameras: [{ camera_id, label, device_id, hostname,
  site_name, status_changed_at }] }`. Empty slices serialize as `[]` not `null`.
  Routed in `api.go` behind `requireAuth` + `onboarded` + site-scope, matching
  `/fleet/alerts`.
- **Services online/total roll-up.** Extend `registry.FleetAlerts` (and the
  `/fleet/alerts` response) with fleet service `online`/`total` counts derived
  from the persisted per-(device, service) service states, rather than adding a
  separate endpoint — `/fleet/alerts` already aggregates service state, so the
  counts ride alongside the existing `services` alert list. The Services gauge
  consumes those counts.
- **Devices-online + Agent-version-conformance use existing data.** Both derive
  client-side from the `GET /devices` summary already loaded by the overview:
  online/total for devices; for version conformance, the share of devices whose
  reported `agent_version` equals the modal/most-common version (or the configured
  desired version) — surfaced as a percentage with the count on the old build.
- **`<Gauge>` (new deep frontend module).** A reusable presentational radial-arc
  SVG component: props for value, max, centre label (percent), sub-label
  (count/status), and tone. Renders the 270° arc, a status-coloured value arc, the
  centred percentage, and an accessible `aria-label`. No data fetching — pure.
- **`gaugeTone(pct)` helper.** Maps a percentage to `success`/`warning` by a
  health threshold so colour assignment is consistent across all four gauges.
- **`<CameraAlertsPanel>` (new component).** Consumes the `/fleet/cameras` hook;
  renders the count badge, the offline list (label, site, humanised outage
  duration from `status_changed_at`), an escalating dot/duration colour by outage
  age, an empty state, and an error state. Mirrors the structure of the existing
  per-device cameras panel.
- **`lib/api` additions.** `getFleetCameras()` + `useFleetCameras()` query hook
  and the wire→client types; the fleet-alerts client extended to carry the new
  service counts.
- **`app/overview/page.tsx` rework.** Replace the `stat-grid` of three tiles with
  the four-gauge row and add `<CameraAlertsPanel>` beside the retained
  `NeedsAttention` panel. The conditional `FleetAlertsPanel` and Clients card
  behaviour is unchanged.
- **Status semantics.** Green = healthy, amber = needs attention; thresholds are a
  presentation detail in `gaugeTone`, not persisted. No red tier on the gauges in
  v1 (red is reserved for the camera-outage duration escalation in the alerts
  list).

## Testing Decisions

A good test asserts external behaviour, not implementation detail — the shape and
contents of a roll-up given seeded data, not the SQL text.

- **`registry.FleetCameras` — integration test (the one required test).** Follows
  the existing registry integration-test prior art (testcontainers Postgres,
  Docker-gated, skips on darwin CI). Seed devices + cameras with mixed statuses
  (online/offline/unknown) across multiple sites; assert the `total`/`online`/
  `offline` counts, the offline list contents (label, hostname, site, changed-at),
  the worst-first ordering, and that site-scoping filters the roll-up correctly.
  Prior art: the existing `registry` integration tests and `FleetAlerts`.
- The remaining modules (`<Gauge>`, `<CameraAlertsPanel>`, `CamerasHandler`) are
  **not** formally test-specced in this PRD — they're verified against the
  approved mockup by running the dashboard. (A handler fake-store test mirroring
  `alerts_test.go` and a `<Gauge>`/panel vitest may be added at the implementer's
  discretion but are not acceptance criteria.)

## Out of Scope

- A fully separate `/fleet/services` endpoint — service counts ride on
  `/fleet/alerts` instead.
- A red/critical gauge tier and configurable per-gauge thresholds — v1 is
  green/amber on a fixed threshold.
- Historical trends / sparklines on the gauges — point-in-time only.
- A fleet camera gallery / live-preview grid (explicitly dropped in ADR-030, not
  revived here).
- Cert health as a gauge — it was considered and dropped from the gauge row;
  cert-expiring stays in the Needs-attention panel.
- Per-camera drill-down beyond the existing per-device cameras page (the panel
  links out to it).

## Further Notes

- Camera reachability data already exists per device (`ListCamerasWithStatus`,
  camera observability #112/#115); this feature only adds the fleet roll-up over
  it — no agent or probe changes.
- The four-gauge layout and the camera-alerts panel were signed off from a
  rendered full-page mockup this session; the radial-arc style was chosen over
  speedometer-dial and linear-meter alternatives.
- Relationship to issue #10 (Edge-UI standalone audio test): unrelated — that is a
  device-local audio test; this is a CP overview enhancement.
