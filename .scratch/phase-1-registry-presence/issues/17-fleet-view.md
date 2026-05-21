# Issue 17 — Fleet view

Status: ready-for-agent
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

- [ ] Logging in as the first-run admin (staff with `*` allowlist) shows all enrolled devices grouped by client/site.
- [ ] Presence chip reflects the device's `is_online` field; flips within at most ~10s of a real state change (one poll cycle).
- [ ] Each device row links to the per-device view URL (the page itself lands in #18; the link is in place here).
- [ ] No `setInterval` in component code; polling is configured exclusively via the query hook.
- [ ] Empty, loading, and error states render correctly (covered by component tests).

## Blocked by

- Issue 07 (presence in API).
- Issue 16 (dashboard scaffold + auth).
