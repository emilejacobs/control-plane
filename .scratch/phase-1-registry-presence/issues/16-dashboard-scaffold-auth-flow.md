# Issue 16 — Dashboard scaffold + auth flow

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 13–17, 22, § Implementation Decisions (dashboard module: `api/queryClient`).
- ADRs: ADR-005 (API-first; dashboard is a thin client), ADR-010 (local credentials + TOTP).

## What to build

The Next.js dashboard scaffold plus the complete auth flow: first-run admin claim, login, mandatory TOTP enrollment, recovery codes shown once. Everything else (fleet view, per-device view) is later slices; this slice gets to "I can log into the dashboard and see an empty 'Devices' shell."

Scope:

- Next.js project scaffold (App Router; TypeScript). Lives in `web/` or equivalent — implementation chooses the directory.
- TanStack Query client wired with a JWT bearer-token interceptor. Refresh-on-401 using the refresh token. Helpers: `useDevices()`, `useDevice(id)`, `useLogin()`, `useFirstRun()`, `useEnrollTotp()`.
- **Structural rule enforced from the start** (per PRD): no `setInterval` in components. Live data flows only through the query layer.
- Login page: email + password + TOTP code, with branching for `requires_totp_enrollment` (route to enrollment).
- First-run page (`/first-run`): visible only if `/auth/first-run` returns 200 (server still accepts); after success, routes to forced TOTP enrollment.
- TOTP enrollment page: shows the provisioning URI as a QR code, takes the user's first valid code, displays the 10 recovery codes once with a "I've saved these" confirmation gate before letting the user proceed.
- Recovery-code-only login path (operator who lost their device can enter a recovery code instead of a TOTP code).
- Empty "Devices" landing page placeholder — populated by #17.

## Acceptance criteria

- [x] On a fresh deployment, navigating to the dashboard URL renders the first-run page; submitting the form creates the admin account and routes to TOTP enrollment.
- [x] TOTP enrollment displays a scannable QR code and the recovery codes; user cannot proceed without confirming they've saved the codes.
- [x] After enrollment, a valid login (email + password + TOTP code) lands on the empty Devices page.
- [x] A recovery code can be used in place of a TOTP code at login; the same recovery code is rejected on a second attempt.
- [x] No `setInterval` exists in any component file (CI check or explicit grep test).
- [x] Tests: minimal E2E (Playwright or equivalent) for the login → TOTP-enroll → dashboard path.
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 05 (TOTP endpoints).

## Comments

### 2026-05-22 — landed in 13 cycles (`13fcec2`..`5bf92b2`)

The Next.js dashboard scaffold + the full auth flow. First non-Go code
in the repo — lives in `web/`.

- Cycle 0: scaffold — Next.js 16 App Router + TypeScript, Vitest/RTL/MSW.
- Cycles 1–2: `lib/api/client` — bearer token, Idempotency-Key on
  mutations, transparent refresh-on-401.
- Cycle 3: `firstRun` API call.
- Cycle 4: `/first-run` page → routes to TOTP enrollment.
- Cycle 5: `login` API call (TOTP code or recovery code).
- Cycles 6–7: `/login` page — `requires_totp_enrollment` branching,
  recovery-code path.
- Cycle 8: `/totp-enroll` page — QR + recovery codes + "I've saved
  these" gate.
- Cycle 9: `/devices` landing shell + `useDevices` hook.
- Cycle 10: no-`setInterval` structural guard.
- Cycle 11: full first-run→enroll→login→Devices flow test.
- Cycle 12: docs + `next build` verification.

**Test strategy.** AC6 says "Playwright **or equivalent**." With user
sign-off, the equivalent is Vitest + React Testing Library + MSW —
component/flow tests in jsdom against a network-mocked cp-api. The
cycle-11 flow test walks all four pages end to end. A true Playwright
run against the live stack was judged disproportionate for a scaffold
slice; it can be added when there is a deployed environment to point at.

**AC4 split.** The dashboard can present a recovery code at login
(cycles 5, 7). The "rejected on a second attempt" half is cp-api's
single-use enforcement, already covered by #05; the dashboard surfaces a
rejected reuse through its normal failed-login error path.

**`useDevice(id)` deferred.** The issue's helper list includes
`useDevice(id)`, but no #16 page consumes it (the per-device view is
#18). Built `useDevices()` (the Devices shell uses it); `useDevice` lands
with #18.

**Documentation criterion.** Discharged — `architecture.md` § Dashboard
describes `web/`, the `lib/api` client, the TanStack-Query/no-setInterval
rule, and the auth flow; the "not yet built" list now scopes the
dashboard to #17/#18. `CONTEXT.md` unchanged — #16 adds no domain term.
No ADR — Next.js + the thin-client dashboard are settled by ADR-005 and
the PRD; the Vitest/MSW test approach is an implementation detail.
