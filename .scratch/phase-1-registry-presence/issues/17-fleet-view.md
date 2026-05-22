# Issue 17 — Fleet view

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 12, 19, § Implementation Decisions (dashboard, presence display).

## What to build

The fleet-view page: a list of all devices the logged-in operator is authorized to see, grouped by client and site, with an online/offline chip per row, polled every 10 seconds.

Scope:

- New page in the dashboard at `/devices` (or equivalent). Replaces the empty placeholder from #16.
- `useDevices()` (TanStack Query) polls `GET /devices` every 10s. Server returns devices already filtered by `scopedDeviceQuery` (#06).
- Render: grouped by client and site, with a presence chip (green dot for online, gray for offline). Each row links to the per-device view (#18).
- Sort: within each site, by hostname.
- Empty state: when the operator's scope returns no devices, show a helpful message (not a blank table).
- Loading state: skeleton or spinner while the first query is in flight.
- Error state: a refresh button if the query fails.

## Acceptance criteria

- [x] Logging in as the first-run admin (staff with `*` allowlist) shows all enrolled devices grouped by client/site.
- [x] Presence chip reflects the device's `is_online` field; flips within at most ~10s of a real state change (one poll cycle).
- [x] Each device row links to the per-device view URL (the page itself lands in #18; the link is in place here).
- [x] No `setInterval` in component code; polling is configured exclusively via the query hook.
- [x] Empty, loading, and error states render correctly (covered by component tests).
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 07 (presence in API).
- Issue 16 (dashboard scaffold + auth).

## Comments

### 2026-05-22 — landed in 7 cycles (`bf7bcb0`..`3d8ff06`)

The fleet-view page — every device the operator may see, grouped by
client and site, polled every 10s.

- Cycle 1 (Go): `registry.List` LEFT JOINs `sites` + `clients`;
  `GET /devices` `listItem` gains nullable `site_name` + `client_name`.
- Cycle 2: `groupDevices` — fleet bucketed by client → site,
  hostname-sorted; the `/devices` page renders nested sections.
- Cycle 3: `PresenceChip` — green/gray dot + Online/Offline text.
- Cycle 4: each row links to `/devices/{id}`.
- Cycle 5: `useDevices` polls every 10s (`refetchInterval`).
- Cycle 6: empty / loading / error states — error has a Refresh button.
- Cycle 7: docs.

**Premise correction.** `GET /devices` returned only
`{device_id, hostname, is_online}` — no site/client to group on. Cycle 1
extended it (a cp-api change, beyond the issue's "new page in the
dashboard" framing).

**Site-less devices → "Unassigned".** `devices.site_id` is nullable and
unset for every device — enrollment (#03) never captures a site, and no
current issue assigns one. So in Phase 1 the whole fleet renders under
one "Unassigned" client/site group. The grouping machinery is correct
and exercised with real multi-group data in the component tests; it will
show true groups once site assignment lands (a future concern, not in
#17/#18). Confirmed with the user before starting.

**Documentation criterion.** Discharged — `architecture.md` § Dashboard
describes the fleet view; #17 dropped from "not yet built". `CONTEXT.md`
unchanged — #17 adds no domain term. No ADR — the "Unassigned" grouping
is a UI behavior, not a hard-to-reverse architectural decision.
