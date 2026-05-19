# ADR-005: API-first design for mobile readiness

**Status:** Accepted (2026-05-05)

**Context.** A mobile app for field operators (installs at client sites) is anticipated. Choices made now in the dashboard backend determine whether mobile is a clean addition or a costly rewrite.

**Decision.** Build the backend as a standalone API service. The dashboard is one client among (eventually) two. Specifically:

- **Auth** — JWT bearer tokens issued by the same flows NextAuth consumes. No server-side sessions exclusive to web.
- **Idempotency** — every state-mutating endpoint accepts an `Idempotency-Key` header.
- **Real-time** — WebSocket channel consumable by web and mobile.
- **Edge UI proxying** — performed by the API service (on the tailnet via subnet router); mobile clients never enroll in the tailnet.
- **Install workflow** — dedicated endpoints (`/enrollments`, `/enrollments/{id}/status`, `/enrollments/{id}/validate`) so a "scan-and-onboard" mobile UX maps directly to them.
- **Notifications** — `notification_targets` table present in the schema from day one even though only WebSocket is wired initially.

**Consequences.**
- (+) Mobile work later means UI + native push integration, not backend refactor.
- (+) Forces a clean, testable API contract independent of UI.
- (+) Web and mobile share a single audit/auth surface — no divergence.
- (-) Marginally more upfront work than a Next.js server-actions monolith (estimate: 1–2 weeks).
- (-) Two services to deploy (API + dashboard), though both are Fargate tasks behind the same ALB.

What is **not** decided now and does not need to be: mobile framework (React Native vs Flutter vs PWA), native UX, app store distribution, push provider (SNS vs direct APNs/FCM). These remain open until mobile work begins.
