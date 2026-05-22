# Issue 18 — Per-device view

Status: ready-for-agent
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

- [ ] Navigating to a device row from #17 lands on a page with all PRD User Story 20 fields populated.
- [ ] Presence chip and `last_seen` ago-string update at the 10s poll cadence; ago-string also re-renders client-side every second.
- [ ] Cert expiry days-remaining is color-coded per #09's thresholds.
- [ ] Component tests cover the loading, error, and empty (cert-expiry near-zero) states.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 17.
