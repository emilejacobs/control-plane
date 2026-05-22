# Issue 18 — Per-device view

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 20–21.

## What to build

The per-device page: static fields (hostname, client, site, hardware kind, OS version, agent version, hardware UUID, IoT Thing ARN, enrollment date), the live presence state, and the mTLS cert expiry (the field surfaces in this slice; the API change is in #09).

Scope:

- New page at `/devices/{id}`. Replaces the empty link target from #17.
- `useDevice(id)` polls `GET /devices/{id}` every 10s.
- Layout: a two-column key/value grid for the static fields, with the presence chip and the cert-expiry indicator near the top so they're impossible to miss.
- Cert expiry indicator: shows the ISO date and days-remaining, color-coded green / yellow / red per #09's thresholds.
- `last_seen` rendered as an ago-string ("12 seconds ago", "3 minutes ago", "2 hours ago"), recomputed client-side from the timestamp returned by the server (so the ago-string ticks even between polls).
- Loading / error states match the fleet view's pattern.

## Acceptance criteria

- [x] Navigating to a device row from #17 lands on a page with all PRD User Story 20 fields populated.
- [x] Presence chip and `last_seen` ago-string update at the 10s poll cadence; ago-string also re-renders client-side every second.
- [x] Cert expiry days-remaining is color-coded per #09's thresholds.
- [x] Component tests cover the loading, error, and empty (cert-expiry near-zero) states.
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 17.

## Comments

### 2026-05-22 — landed in 13 cycles (from `da6a64f`)

The per-device view — one device's static record, live presence, the
`last_seen` ago-string, and the mTLS cert-expiry indicator, polled every
10s at `/devices/{id}`.

- Cycle 1 (Go): `GET /devices/{id}` surfaces `site_name`/`client_name`.
- Cycle 2: tracer bullet — `/devices/[id]` page renders the hostname;
  `getDevice` + `useDevice`.
- Cycle 3: the static `<dt>/<dd>` field grid (PRD User Story 20).
- Cycle 4: site-less device shows "Unassigned".
- Cycle 5: presence chip.
- Cycle 6: `last_seen` ago-string (`formatAgo`).
- Cycle 7: "Never" for a device that has not reported.
- Cycle 8: ago-string ticks every second (`useNow`).
- Cycle 9: cert-expiry indicator (date + days).
- Cycle 10: cert color-coding (red <30, yellow 30–180, green >180).
- Cycle 11: cert-expiry "unknown" for pre-migration rows.
- Cycle 12: loading / error states.
- Cycle 13: docs.

**Premise correction #1 — client/site.** The issue lists client and site
among the static fields, but `GET /devices/{id}` did not return them —
`registry.GetByID` never joined `sites`/`clients` (only `List` did, the
correction #17 made for the fleet list). Cycle 1 mirrored that LEFT JOIN
in `GetByID` and added `site_name`/`client_name` to the handler response.
Confirmed with the user before starting. As in #17, every device's
`site_id` is still null, so client/site render "Unassigned" for now.

**Premise correction #2 — `last_seen`.** The issue says the ago-string is
"recomputed client-side from the timestamp returned by the server." The
#07 API returns `last_seen_ago_seconds` (a relative integer), not an
absolute timestamp. `getDevice` anchors it into an absolute `lastSeenAt`
at fetch time; `useNow` (a TanStack Query `refetchInterval` clock, not a
`setInterval`) drives the per-second re-render. No #07 API change needed.

**#09 ACs 3–4 discharged.** The per-device cert UI and the "renders red"
dashboard test belong to #09's open ACs — both land here (cycles 9–10).
#09 is updated to done.

**Documentation criterion.** Discharged — `architecture.md` § Dashboard
describes the per-device view; #18 moved from "not yet built" to landed.
`CONTEXT.md` unchanged — #18 adds no domain term ("per-device view" was
already in use; "ago-string" is a UI rendering detail). No ADR — the
per-device view is UI, and the `GET /devices/{id}` site/client join
mirrors the existing #17 pattern rather than being a hard-to-reverse
decision.
