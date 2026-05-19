# ADR-010: Local credentials only; drop Entra ID, NextAuth, and Cognito

**Status:** Accepted (2026-05-18)

**Supersedes:** [ADR-006](./0006-nextauth-dual-source-auth.md)

**Context.** ADR-006 specified NextAuth with dual providers (Entra ID OIDC for staff, local Credentials for operators). Two subsequent decisions changed the constraint surface:

1. The API service is Go (ADR-009) — NextAuth (Node-only) cannot live in the API. If retained, NextAuth would have to live on the Dashboard, coupling token issuance to the Dashboard's availability — exactly the coupling ADR-005 ("API-first for mobile") was designed to avoid.
2. The goal is one auth path for all users, optimised for simplicity.

The remaining options were: Cognito for everyone, local credentials in the Go API for everyone, or direct Entra OIDC + local creds in Go. The deciding question was whether automatic deactivation of staff via the corporate IdP is load-bearing for a 63-device internal tool managed by a tiny team. It is not — manual disable on offboarding is acceptable at this scale.

**Decision.** The Go API hosts authentication directly. All users — uKnomi staff and (future) field operators — authenticate the same way: username + Argon2id-hashed password + mandatory TOTP (RFC 6238). No external IdP. The Go API issues short-lived JWTs (~1h) signed with a key in KMS; refresh tokens stored server-side allow re-issuance without re-prompting for credentials.

Authorization is unchanged from ADR-006: JWTs carry a `role` claim (`staff` for full fleet, `operator` for site-scoped) and (for `operator`) a site allowlist. Authz is enforced server-side on every endpoint.

**Consequences.**
- (+) Smallest possible moving parts: no Entra, no Cognito, no NextAuth. One auth surface.
- (+) Mobile auth flow is identical to web (POST username/password+TOTP → JWT). ADR-005's mobile-readiness goal is preserved.
- (+) Go API owns the full lifecycle: registration, password reset, TOTP enrollment, lockout, revocation.
- (-) uKnomi staff do not get corporate SSO; they hold a CP-specific password rather than using Microsoft credentials.
- (-) Offboarding requires manual deactivation in the CP. Acceptable at staff team size.
- (-) Password reset, TOTP enrollment, lockout, and rate-limiting all live in our code. Standard libraries handle the cryptographic primitives (Argon2id, TOTP).

**Verification.** TBD — added at implementation. Integration tests cover: registration, password+TOTP login, JWT issuance + verification, refresh-token rotation, role enforcement, site-allowlist enforcement on every operator-accessible endpoint, lockout after N failed attempts.
