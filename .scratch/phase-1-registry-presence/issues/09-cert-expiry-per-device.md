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

- [x] `GET /devices/{id}` returns `mtls_cert_expires_at` and `mtls_cert_days_remaining`.
- [x] Integration test asserts `days_remaining` is computed correctly relative to a fake "now" against a fixed cert expiry.
- [ ] Per-device UI displays both fields with the documented color coding.
- [ ] Test on the dashboard side verifies that a cert expiring in 10 days renders red.
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 07 (`GET /devices/{id}` exists and returns presence-derived fields).
- Issue 18 (per-device UI exists to display the field).

## Comments

### 2026-05-21 — API slice landed in 5 cycles (`a9e2208`..`bfcb923`); UI ACs await #18

The server side of cert-expiry is done. Two unchecked ACs (per-device UI
+ the dashboard "renders red" test) are genuinely blocked by #18 — the
per-device view doesn't exist yet — so this issue stays open and an agent
picks up ACs 3–4 once #18 lands. The colour thresholds (green >180,
yellow 30–180, red <30) are a pure UI concern; the API just emits the
integer.

- Cycle 1: `GET /devices/{id}` surfaces `mtls_cert_expires_at`.
- Cycle 2: `mtls_cert_days_remaining`, computed at response time from an
  injectable clock — whole days, truncated toward zero.
- Cycle 3: regression test — an expired cert yields a negative
  days-remaining (the value the red colour-coding keys on). Green on
  arrival; cycle 2's signed arithmetic already covered it.
- Cycle 4: persistence. The cert `notAfter` was minted at enrollment but
  discarded — only `mtls_cert_arn` was stored. Migration `006` adds
  `devices.mtls_cert_expires_at` (nullable; every post-006 enrollment
  populates it); `Enroll` persists `cert.ExpiresAt`; `GetByID` reads it.
- Cycle 5: docs.

**Premise correction.** The issue said cert expiry was "already minted at
enrollment in #03 and stored in the `devices` row." It was minted (and
returned to the enrolling device) but *not* persisted — hence the
migration + `Enroll` change in cycle 4.

**Documentation criterion.** Discharged — `architecture.md` (Storage
section, module table, "not yet built" list) updated in cycle 5.
`CONTEXT.md` unchanged: cert TTL is an existing concept owned by ADR-013;
no new domain term. No ADR — ADR-013 already settles the 1-year TTL and
names expiry visibility as the early-warning signal; #09 only implements
it.
