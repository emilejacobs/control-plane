# Issue 01 — First light: heartbeat round-trip on developer laptop

Status: ready-for-agent

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

The first end-to-end tracer bullet for the Phase 0 agent. Stand up everything needed to publish a `heartbeat` command from a developer-laptop CLI to a Go agent running locally, have the agent respond, and see the response printed by the CLI — all via real AWS IoT Core over MQTT-over-WSS with per-device mTLS.

Scope:

- Repository skeleton: `go.mod`, directory layout for the modules sketched in the PRD (`mqtt-transport`, `command-dispatcher`, `agent-cli`, agent binary entry point).
- `mqtt-transport` module: mTLS connect to IoT Core, subscribe + publish, automatic reconnect with exponential backoff. Transport-agnostic interface (`Subscribe(topic, handler)`, `Publish(topic, payload)`) so the dispatcher can be tested against a fake transport. Owns no business logic; bytes in / bytes out.
- `command-dispatcher` module: handler registry, JSON envelope decode/encode, dispatch by `type`, uniform error wrapping, `correlation_id` propagation from request to response.
- `heartbeat` handler returning `{device_id, version, os, uptime_seconds}`.
- Agent binary entry point that reads a config file (cert path, key path, broker URL, device id), constructs transport + dispatcher, registers the heartbeat handler, subscribes to `devices/{id}/cmd`, runs.
- `agent-cli` developer tool: reads developer IAM credentials, connects to IoT Core, publishes a chosen command type + args to a chosen device id, subscribes to `devices/{id}/cmd-result`, prints the response (or times out).
- Structured JSON logging via `log/slog` with required fields per ADR-011 (`ts`, `level`, `service`, `correlation_id`, `msg`). `correlation_id` from the inbound command envelope is propagated through every log call in the handler and echoed in the response envelope.
- IoT Core manual provisioning runbook (markdown, in `docs/runbooks/` or similar): create CA, register CA with IoT Core, generate per-device cert signed by the CA, register one thing for the developer laptop, attach an IoT policy that allows publish/subscribe on the device's topics only. Precise enough that a fresh execution from the steps produces a working dev-laptop "device" without ad-hoc reasoning.
- Agent refuses to start with a clear error if cert is missing, malformed, or expired.

Envelope schemas are defined in the PRD's Implementation Decisions section — implement exactly that shape (including the reserved `signature: null` field for Phase 3 signing).

Tests (per ADR-012):

- Fake-broker integration tests for `mqtt-transport` covering connection lifecycle, reconnect-after-disconnect, topic routing. Test broker is either the Paho in-process test mode or a containerised Mosquitto via `testcontainers-go`. CI must not require real IoT Core.
- Unit tests for `command-dispatcher` covering: known-command success, unknown-command rejection with stable error code, handler panic caught and surfaced as failure envelope, `correlation_id` from request appears in response, malformed JSON envelope rejected without crashing.
- Envelope serialise/deserialise roundtrip property test recommended.

## Acceptance criteria

- [ ] `go build` produces an agent binary that connects to AWS IoT Core using a manually-provisioned mTLS cert and config file.
- [ ] `agent-cli heartbeat <device-id>` published from the developer laptop receives a response within 2 seconds and prints the device_id, version, os, and uptime returned by the agent.
- [ ] The `correlation_id` in the request envelope appears in the response envelope and in every log line for that command's handling (verified by inspecting the agent's structured logs).
- [ ] Killing the network on the agent host and restoring it causes the agent to reconnect automatically without manual intervention.
- [ ] The agent refuses to start with a clear error if the cert file is missing or expired.
- [ ] The IoT Core provisioning runbook exists and is precise enough that a fresh execution from the steps produces a working dev-laptop "device".
- [ ] Tests as described above pass via `go test ./...`.
- [ ] Structured JSON logs are emitted via `log/slog` with all required fields per ADR-011.

## Blocked by

None — can start immediately.
