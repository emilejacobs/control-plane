# ADR-009: Go for the API service

**Status:** Accepted (2026-05-18)

**Context.** Two languages are already in play: Go for the agent (ADR-002) and TypeScript for the Next.js dashboard. The API service is the third significant runtime. `architecture.md` originally recommended Node/TS for the API, citing velocity and type-sharing with the dashboard.

Under the actual operating model (AI-agent dev, tiny team, no human-team bus-factor concerns), the trade-offs flip:

- The agent and API both implement the signed-command protocol. With two languages, the payload schema, sign/verify code, and dispatcher state live in two type systems — divergence risk.
- Three runtime stacks (Go agent + Node API + Next.js dashboard) means three CI toolchains, three dep-vuln streams, three sets of build artefacts. Operational tax is real for a small team.
- Type-sharing API↔Dashboard via NextAuth is moot (see ADR-010). Type-sharing Go→TS is solvable via OpenAPI codegen (or `tygo`) and is arguably cleaner: an explicit, versioned contract instead of an implicit shared types package.

**Decision.** The Control Plane API service is written in Go. The API publishes an OpenAPI spec as the contract of record; the Next.js dashboard generates a typed TS client from that spec as part of its build.

**Consequences.**
- (+) Agent and API share Go primitives for the signed-command protocol — one definition of truth.
- (+) Two runtime stacks instead of three (Go for backend, TS for dashboard).
- (+) Explicit, versioned API contract via OpenAPI; less implicit coupling.
- (-) Dashboard loses the "free" type sharing a Node API would provide. OpenAPI codegen adds a build step.
- (-) Node ecosystem ergonomics for rapid CRUD scaffolding (Prisma/Drizzle/NextAuth) are not available; Go equivalents are used (sqlc/sqlx; custom auth — see ADR-010).

**Verification.** TBD — added at implementation. CI checks that the committed OpenAPI spec is current with the Go handlers; dashboard build fails if the generated client is out of date.
