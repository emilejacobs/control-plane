# Issue 05 — Mandatory TOTP + recovery codes

Status: done
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

- [x] A freshly-created operator can call `POST /auth/totp/enroll` once and receive a provisioning URI + 10 recovery codes; a second call returns 409.
- [x] After enrollment, `POST /auth/login` requires a valid TOTP code; valid codes within the ±1 window are accepted, codes outside are rejected.
- [x] A recovery code can be used in place of a TOTP code on `POST /auth/login`, and a used recovery code is rejected on subsequent use.
- [x] Until TOTP enrollment is completed, all authenticated endpoints (other than `/auth/totp/enroll`) return 403 with the documented reason code.
- [x] TOTP secrets in the DB are encrypted at rest (test asserts the column does not contain the raw secret).
- [x] Unit tests cover the TOTP window behavior with a fake clock.
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 04.

## Comments

### 2026-05-21 — landed in 11 cycles (`84eb111`..`101dd0d`)

Mandatory TOTP + 10 single-use recovery codes, extending the `AuthN`
module from #04.

- Cycle 1: TOTP primitive — `github.com/pquerna/otp` (RFC 6238, per
  ADR-010's "standard libraries handle TOTP") wrapped clock-parameterized.
- Cycle 2: lock the ±1 drift window (AC6) — green on arrival, cycle 1's
  shared opts already set `Skew=1`.
- Cycle 3: `aeadCipher` — AES-256-GCM for secrets at rest.
- Cycle 4: migration `007` (`operators.totp_secret_encrypted`,
  `recovery_codes_hashed`) + `EnrollTotp` + `POST /auth/totp/enroll`.
- Cycle 5: enrollment is once-only — 409 (AC1).
- Cycle 6: assert the secret is encrypted at rest (AC5) — green on
  arrival, cycle 4 already encrypts.
- Cycle 7: `Login` requires a valid TOTP for an enrolled operator (AC2).
- Cycle 8: recovery-code login, single-use (AC3).
- Cycle 9: `RequireTotpEnrolled` gate — 403 + `Reason` header (AC4).
- Cycle 10: `requires_totp_enrollment` on the login response.
- Cycle 11: docs.

**Encryption at rest.** The issue specified a "KMS data key envelope."
Implemented instead — with explicit user sign-off — as AES-256-GCM with a
32-byte key from `TOTP_ENCRYPTION_KEY`, a KMS-protected secret loaded at
startup, exactly the handling the JWT signing key already uses. Avoids
live KMS calls in the request path and in every auth test; "envelope"
holds at the ops layer (KMS protects the key, AES protects the data).
No separate ADR — the encryption mechanism is an implementation detail
under ADR-010 (mandatory TOTP); the choice and rationale are recorded
here and in `architecture.md` § Auth.

**Premise note.** `VerifyTotp` / `ConsumeRecoveryCode` from the PRD's
`AuthN` interface sketch are unexported helpers (`validateTotp`,
`consumeRecoveryCode`) — the HTTP-facing surface is `Login` + `EnrollTotp`,
and every behavior is tested through it. `mintAccessToken` (test helper)
now backs its token with a real enrolled operator, since gated read
endpoints require one.

**Documentation criterion.** Discharged — `architecture.md` (Auth
section, module table) updated in cycle 11. `CONTEXT.md` unchanged: the
"Recovery code", "First-run admin", and TOTP-carrying "Operator" entries
already cover the domain terms; #05 adds no new one. No ADR (see above).
