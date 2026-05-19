# ADR-012: Test policy — standard pyramid + mandatory integration tests + CI gate

**Status:** Accepted (2026-05-18)

**Context.** Under AFK-agent dev with architectural-reviewer-only humans (no per-PR human reviewer for non-architectural changes), tests are the primary defence against regressions. Code review will not catch what tests miss. Test coverage policy must be explicit and CI-enforced from the first commit.

**Decision.**

- **Unit tests** for non-trivial pure functions (parsers, validators, signature primitives).
- **Integration tests** for every API endpoint:
  - Success path covering the documented response shape.
  - Meaningful failure modes (auth failure, validation failure, not-found, conflict).
  - For every state-mutating endpoint: an idempotency test asserting that repeating a request with the same `Idempotency-Key` produces the same result and does not duplicate state.
- Integration tests run against real Postgres (testcontainers), LocalStack KMS, and a fake/test IoT Core MQTT broker.
- **End-to-end tests** cover the headline flows (enrollment, command execution, Edge UI proxy) at least once.
- **CI gate.** Merge to main requires all tests green. An AFK agent that flags a PR `ready-for-human` must confirm CI is green; failing CI means the PR is not ready.
- Property-based and adversarial tests are recommended but not mandatory. The implementing agent should reach for these tools when the surface justifies it (signed-command verification, JWT validation).

**Consequences.**
- (+) Regressions in core flows are caught before merge, not in production.
- (+) The idempotency requirement (ADR-005) is enforced by test, not by convention.
- (+) Real Postgres + LocalStack catch entire classes of bug that mocks hide.
- (-) Test infrastructure (testcontainers, LocalStack) adds CI complexity and runtime.
- (-) Faster iteration is sacrificed for safety; the right trade under AFK-agent dev.

**Verification.** TBD — added at implementation. CI configuration enforces the gate. A lint or convention check verifies that every handler under `internal/api/handlers/` has at least one integration test in the parallel test directory.
