# Issue 09 — Cert expiry surfaced on per-device view

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 21.
- ADR: ADR-013 (1-year cert TTL in Phase 1; cert rotation in Phase 4 — expiry visibility is the early-warning signal).

## What to build

The mTLS cert expiry — already minted at enrollment in #03 and stored in the `devices` row — is surfaced on `GET /devices/{id}` as both an ISO-8601 timestamp and a computed `days_remaining` integer. The dashboard per-device view (#18) renders both. The slice exists separately because expiry is the early-warning signal if Phase 4 (cert rotation) slips, and shipping it is cheap insurance.

Scope:

- `GET /devices/{id}` response shape adds `mtls_cert_expires_at` (ISO 8601) and `mtls_cert_days_remaining` (computed at response time).
- Per-device UI (#18) surfaces both. Color the days-remaining indicator: green (>180), yellow (30–180), red (<30).
- No alarm wired here — alarms are #21.

## Acceptance criteria

- [ ] `GET /devices/{id}` returns `mtls_cert_expires_at` and `mtls_cert_days_remaining`.
- [ ] Integration test asserts `days_remaining` is computed correctly relative to a fake "now" against a fixed cert expiry.
- [ ] Per-device UI displays both fields with the documented color coding.
- [ ] Test on the dashboard side verifies that a cert expiring in 10 days renders red.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 07 (`GET /devices/{id}` exists and returns presence-derived fields).
- Issue 18 (per-device UI exists to display the field).
