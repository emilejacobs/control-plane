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

**Verification.**

- CI gate: `.github/workflows/ci.yml` runs `go test ./...` and the cross-build matrix on every push and pull request. Branch protection on `main` requires the three matrix jobs (`darwin/arm64`, `darwin/amd64`, `linux/arm64`) as required status checks and requires changes to go through a pull request.
- Local equivalent: `Makefile` `ci` target (`make ci`).
- Dispatcher unit tests (success path, unknown-command-type, handler panic, plain-error wrap, `CodedError` unwrap, correlation_id propagation, correlation_id logging): `internal/dispatcher/dispatcher_test.go` (8 tests).
- Transport tests against a real Mosquitto broker via `testcontainers-go` — the Phase 0 stand-in for the "fake/test IoT Core MQTT broker" requirement: `internal/transport/transport_test.go::TestTransportRoundtrip` and `::TestTransportNewFailsWhenBrokerUnreachable` (helpers in `internal/transport/helpers_test.go`).
- Handler unit tests via the `service.Fake` (per the PRD's testing decisions — unit-mocking shell invocations is avoided):
  - `internal/handlers/heartbeat/heartbeat_test.go::TestHeartbeatReturnsExpectedFields`
  - `internal/handlers/servicestatus/servicestatus_test.go` (3 tests, covering Running, Stopped, and the `service.not_found` code)
  - `internal/handlers/servicerestart/servicerestart_test.go::TestRestartReturnsTimestampsOnSuccess` and `::TestRestartFailureSurfacesAsServiceRestartFailedCode`
- Agent end-to-end command + telemetry dispatch over fake transport: `internal/agent/agent_test.go::TestAgentDispatchesCommandsAndPublishesResults`, `::TestAgentDispatchesServiceStatus`, `::TestAgentDispatchesServiceRestart`, `::TestAgentPublishesTelemetryHeartbeats`.
- Telemetry resilience (panicking collector does not crash the publisher; subsequent ticks still fire): `internal/telemetry/publisher_test.go::TestPublisherSurvivesPanickingCollector`.
- Idempotency tests for mutating API endpoints (per the Decision's third bullet): **N/A in Phase 0** — no API endpoints exist yet. The convention lands with ADR-009's implementation in Phase 1.
- Handler-coverage lint (every handler under `internal/api/handlers/` has at least one integration test in the parallel test directory): **deferred to Phase 1** — the `internal/api/handlers/` directory does not exist yet. Follow-up issue to be opened when the API service is scaffolded.
- LocalStack KMS + testcontainers Postgres integration tests: **N/A in Phase 0** — no KMS or DB dependencies yet. Land with ADR-009 and ADR-015 implementations.
