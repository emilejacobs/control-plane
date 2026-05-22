# Issue 16 — Dashboard scaffold + auth flow

Status: ready-for-agent
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

- [ ] On a fresh deployment, navigating to the dashboard URL renders the first-run page; submitting the form creates the admin account and routes to TOTP enrollment.
- [ ] TOTP enrollment displays a scannable QR code and the recovery codes; user cannot proceed without confirming they've saved the codes.
- [ ] After enrollment, a valid login (email + password + TOTP code) lands on the empty Devices page.
- [ ] A recovery code can be used in place of a TOTP code at login; the same recovery code is rejected on a second attempt.
- [ ] No `setInterval` exists in any component file (CI check or explicit grep test).
- [ ] Tests: minimal E2E (Playwright or equivalent) for the login → TOTP-enroll → dashboard path.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 05 (TOTP endpoints).
