# Issue 05 — Mandatory TOTP + recovery codes

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 15–17, § Implementation Decisions (AuthN extensions, recovery codes).
- ADR: ADR-010 (local credentials with mandatory TOTP).

## What to build

Mandatory TOTP enrollment on first session plus 10 single-use recovery codes. Extends the `AuthN` module from #04.

Scope:

- Schema additions to `operators`: `totp_secret_encrypted bytea`, `recovery_codes_hashed text[]`. Migration adds them; existing rows from #04 are reconciled (the first-run admin must enroll TOTP on next login).
- `AuthN` extensions: `EnrollTotp(operator) → ProvisioningURI + RecoveryCodes`, `VerifyTotp(operator, code) → bool`, `ConsumeRecoveryCode(operator, code) → bool`. TOTP secret encrypted at rest (KMS data key envelope). Recovery codes shown once, stored Argon2id-hashed, single-use.
- `POST /auth/totp/enroll` — authenticated, allowed only when the operator has no TOTP secret yet. Returns provisioning URI (otpauth:// for the authenticator app to render as QR) and the 10 recovery codes. Calling a second time fails.
- `POST /auth/login` extended: now requires a third field `totp_code` (or a `recovery_code`). The login response from #04 changes — clients must handle a `requires_totp_enrollment` flag and route to the enrollment flow before completing login.
- A first-login forced-enrollment gate: any authenticated request other than `/auth/totp/enroll` returns 403 with `Reason: totp-enrollment-required` if the operator has no TOTP secret yet.

## Acceptance criteria

- [ ] A freshly-created operator can call `POST /auth/totp/enroll` once and receive a provisioning URI + 10 recovery codes; a second call returns 409.
- [ ] After enrollment, `POST /auth/login` requires a valid TOTP code; valid codes within the ±1 window are accepted, codes outside are rejected.
- [ ] A recovery code can be used in place of a TOTP code on `POST /auth/login`, and a used recovery code is rejected on subsequent use.
- [ ] Until TOTP enrollment is completed, all authenticated endpoints (other than `/auth/totp/enroll`) return 403 with the documented reason code.
- [ ] TOTP secrets in the DB are encrypted at rest (test asserts the column does not contain the raw secret).
- [ ] Unit tests cover the TOTP window behavior with a fake clock.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 04.
