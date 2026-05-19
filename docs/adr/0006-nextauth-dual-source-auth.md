# ADR-006: NextAuth dual-source auth, no Cognito

**Status:** Superseded by [ADR-010](./0010-local-credentials-only.md) (2026-05-18)

**Context.** Two operator populations: uKnomi staff (Microsoft 365 / Entra ID) and future field operators (third-party technicians, transient, scoped to specific sites). Auth-broker options: AWS Cognito with Entra federation, Auth0/Clerk, NextAuth with multiple providers.

**Decision.** NextAuth with two providers side-by-side: **Entra ID OIDC** for staff (`role=staff`), **Credentials** for local accounts (`role=operator`, Argon2-hashed passwords, mandatory TOTP). Operator JWTs carry a site allowlist enforced server-side on every endpoint.

**Consequences.**
- (+) Staff identity stays in the corporate IdP — single revocation point on offboarding.
- (+) Operator accounts are scoped, granular, and don't pollute Entra.
- (+) NextAuth handles both providers natively; no separate auth broker.
- (+) Schema includes `role` and `source` from day one — no migration when operators arrive.
- (-) Local credential management (password reset, TOTP enrollment) is on us. Acceptable given the small operator population.
- (-) No AWS-native identity for direct AWS SDK use from clients. Not needed; operators interact only with the CP API.

Cognito was rejected as overhead operators don't need to interact with. Auth0/Clerk rejected for cost and an additional vendor relationship not justified at this scale.
