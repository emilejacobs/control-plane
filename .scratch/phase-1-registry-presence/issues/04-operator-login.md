# Issue 04 — Operator login (first-run admin + password + JWT)

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 13–14, 17–18, § Implementation Decisions (AuthN module, schema for `operators` and `refresh_tokens`, API contracts for `/auth/*`).
- ADRs: ADR-010 (local credentials only), ADR-009 (Go API service), ADR-011 (structured logs).

## What to build

The `AuthN` deep module plus the first three `/auth/*` endpoints — enough to bootstrap an admin account on first deploy and log in with username + password (JWT bearer + refresh). TOTP is added as a separate slice (#05) so this one stays focused.

Scope:

- Postgres schema for `operators` (per PRD schema sketch, minus TOTP-related columns) and `refresh_tokens`. Both via migrations.
- `AuthN` module owns: Argon2id password hashing + verification (parameters pinned in code), JWT issuance + signing (HS256 or RS256 — decide at implementation time), refresh token issuance + rotation (refresh stored hashed, revocable), first-run-admin lifecycle (a `system_initialized` flag; `/auth/first-run` accepts a request only when false; flips irreversibly on success). Per-account lockout (5 failed attempts → 15 min) lives here.
- `POST /auth/first-run` — body `{email, password}`. Returns 201 on success with admin operator + initial JWT; 410 Gone if `system_initialized == true`. Audit-log records source IP, UA, email, outcome.
- `POST /auth/login` — body `{email, password}`. Returns `{access_token (1h), refresh_token (24h)}`. Lockout enforced. ALB-level per-IP rate limit (60 req/min) configured via Terraform alongside the endpoint.
- `POST /auth/refresh` — body `{refresh_token}`. Rotates the token; previous refresh becomes invalid.
- Auth middleware that validates bearer tokens and injects the operator into request context. Applied to `GET /devices/*` (which was unauthenticated in #03 — flip the dev-only feature flag off).

No TOTP yet (#05). No site-scoping yet (#06).

## Acceptance criteria

- [x] `POST /auth/first-run` on a fresh deployment creates the admin account and returns a JWT; a second call returns 410 Gone.
- [x] `POST /auth/login` with correct credentials returns an access + refresh token; with wrong credentials, returns 401 and increments the lockout counter; 5 failures within the window locks the account for 15 min.
- [x] `POST /auth/refresh` rotates tokens; the previous refresh token is rejected on subsequent use.
- [x] `GET /devices/*` without a valid bearer returns 401; with a valid bearer returns the row.
- [x] Integration tests cover the full flow against Postgres test container with a fake clock for lockout-window expiry.
- [x] Audit log entries are written for first-run claim, login (success and failure), and refresh.

## Blocked by

- Issue 03 (HTTP skeleton, idempotency middleware, audit log middleware foundation).

## Comments

### 2026-05-21 — landed in 9 TDD cycles (`f08ecd3`..`aeafb67`)

`AuthN` deep module (`internal/cp/authn`) + `/auth/*` endpoints + bearer
auth middleware. HS256 JWTs (`golang-jwt/jwt/v5`) over an env-loaded key;
Argon2id passwords (`alexedwards/argon2id`, OWASP-2024 params). `operators`
and `refresh_tokens` schema via migrations 003/004.

- Cycles 1–2: `HashPassword`/`VerifyPassword` and the JWT `Signer`
  (sign + verify round-trip), unit-tested.
- Cycle 3: `POST /auth/first-run` — `ClaimFirstRunAdmin` bootstraps the
  admin (no `system_initialized` table; `EXISTS(operators)` + UNIQUE on
  email handles the race), returns a token pair.
- Cycle 4: second first-run → 410 Gone, no second operator row.
- Cycles 5–6: `POST /auth/login` — Argon2id verify, `last_login_at`
  stamp, token pair; wrong password / unknown email → 401, failed
  attempts counted per account.
- Cycle 7: lockout — 5 failures lock for 15 min; `ErrAccountLocked`
  → 429. `AuthN` clock is injectable (`Config.Now`); lockout timestamps
  computed in Go so the integration test drives window expiry with a
  fake clock.
- Cycle 8: `POST /auth/refresh` — rotation via a conditional UPDATE
  (replayed/expired token affects no rows, one rotation wins).
- Cycle 9: `middleware.Auth` guards `GET /devices/{id}`; the
  `CP_DEV_DEVICES_GET` dev escape hatch from #03 is removed. cp-api
  loads the signing key from `JWT_SIGNING_KEY` (base64, ≥32 bytes).

Audit-shaped log lines (`audit.first_run`, `audit.login`,
`audit.refresh`) flow through `cplog`, searchable by correlation_id.

**Deferred — ALB per-IP rate limit (60 req/min) on `/auth/login`.** The
scope notes it as Terraform "alongside the endpoint", but no ALB module
exists in Phase 1 yet. Track with the Terraform work in #01; the
application code is in place. TOTP (#05) and site-scoping (#06) are
separate slices by design.
