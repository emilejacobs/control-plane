# PRD: Phase 0 — Cross-platform agent spike

Status: ready-for-agent
Phase: 0 (per `docs/roadmap.md`)
Created: 2026-05-18

## Problem Statement

As the uKnomi team, we are about to commit weeks of work to building the Control Plane API, Dashboard, AWS infrastructure, and a 63-device rollout — all on top of architectural assumptions that have not been validated against reality. Specifically: we assume that an MQTT-over-WSS connection from a Go agent through a real client-site NAT to AWS IoT Core will work reliably, that a single Go binary can serve both macOS and Linux without per-OS patches, and that command round-trips will complete fast enough to feel responsive. If any of these assumptions is wrong, we want to know now — not in Phase 1 after the infrastructure is built and 63 devices are pending rollout.

## Solution

Build the smallest possible vertical slice that exercises the architecture end-to-end:

- A Go `uknomi-agent` binary cross-compiled for `darwin/arm64`, `darwin/amd64`, and `linux/arm64`.
- A persistent MQTT-over-WSS connection from the agent to AWS IoT Core using per-device mTLS (manually provisioned for this phase).
- Three commands wired end-to-end through the command channel: `heartbeat`, `service.status <name>`, `service.restart <name>`.
- A periodic telemetry heartbeat on a separate topic so liveness can be observed even when no command is in flight.
- A small developer-laptop CLI (`agent-cli`) that publishes commands to a chosen device id and prints responses — this stands in for the Phase 1 CP API.
- Deployment to at least one real Mac Mini at a client site and one Linux device (Pi or Radxa, lab or client site) for cross-platform validation.

The Control Plane API service, the Dashboard, and the enrollment endpoint are explicitly out of scope. The CLI is a throwaway harness whose only job is to prove the agent works without waiting for Phase 1.

## User Stories

Actors: **Developer** (the AI agent doing the implementation and the human reviewing architecture-touching changes), **Operator** (uKnomi staff member whose production device hosts the Phase 0 deployment), **Edge device** (the Mac Mini / Pi where the agent runs).

1. As a developer, I want a single Go codebase that cross-compiles to `darwin/arm64`, `darwin/amd64`, and `linux/arm64`, so that I can verify cross-platform support is a build-flag concern and not a code-rewrite concern (per ADR-002).
2. As a developer, I want the agent to maintain a persistent MQTT-over-WSS connection to AWS IoT Core using per-device X.509 mTLS, so that I can prove the command channel works through real client-site NAT (per ADR-001).
3. As a developer, I want the agent to automatically reconnect to IoT Core after network interruptions using exponential backoff, so that I can validate the system survives the unreliable connectivity typical of client sites.
4. As a developer, I want the agent to refuse to start if its mTLS cert is missing, malformed, or expired, so that misconfiguration fails fast and visibly rather than producing a silently-broken device.
5. As a developer, I want the agent's connection config (cert path, key path, broker URL, device id) to come from a config file (or env vars) and not be hardcoded, so that the same binary deploys to any device by changing only its config.
6. As a developer, I want a `heartbeat` command that returns the agent's basic identification (device id, version, OS, uptime), so that I can prove the simplest possible round-trip works.
7. As a developer, I want a `service.status <name>` command that returns the running state of a named service (via `launchd` on Mac, `systemd` on Linux), so that I can prove the OS-abstraction layer works on both platforms with the same binary.
8. As a developer, I want a `service.restart <name>` command that restarts a named service and reports success/failure with the underlying tool's output, so that I can prove command execution with side effects works end-to-end.
9. As a developer, I want every command request and response to carry a `correlation_id` field as a required part of the envelope (per ADR-011), so that the logging convention is established from day one and not retrofitted later.
10. As a developer, I want the agent to log in structured JSON via `log/slog` with `correlation_id` propagated end-to-end (per ADR-011), so that future debugging — even at Phase 0 — uses the right telemetry shape.
11. As a developer, I want command round-trips to complete in under 2 seconds when the device is online, so that the success criterion from `roadmap.md` is met.
12. As a developer, I want the exact same compiled binary to work on both the test Mac and the test Linux device, so that I can confirm the build-tag separation works and no Linux-specific patches are needed.
13. As a developer, I want a `agent-cli` tool on my laptop that connects to IoT Core with developer credentials, publishes a chosen command to a chosen device id, and prints the response, so that I can drive the agent without building the Phase 1 CP API.
14. As a developer, I want a runbook for manually provisioning an IoT Core thing + mTLS cert for a device, so that I can deploy Phase 0 without waiting for the Phase 1 enrollment endpoint (per ADR-004).
15. As a developer, I want minimal LaunchDaemon (macOS) and systemd unit (Linux) files for the agent, so that the agent runs on boot and restarts on crash on both platforms.
16. As a developer, I want command handlers to be registered through a `command-dispatcher` registry, so that adding new commands in Phase 1+ is a one-line registration and not a refactor.
17. As a developer, I want the `mqtt-transport` module to expose a transport-agnostic `Subscribe(topic, handler)` / `Publish(topic, payload)` interface, so that the `command-dispatcher` can be tested against a fake transport with no AWS dependency.
18. As a developer, I want the `service-backend` module's interface (`Status(name)`, `Restart(name)`) to be identical on Mac and Linux with only the implementation differing via build tags, so that command handlers never branch on OS.
19. As a developer, I want a periodic heartbeat published to `devices/{id}/telemetry` at a configurable interval (default 30s) carrying device id, agent version, uptime, and the current correlation context, so that liveness can be observed independently of in-flight commands.
20. As a developer, I want the command envelope JSON schema to reserve a `signature` field (unused in Phase 0, populated by Phase 3 Ed25519 signing), so that the protocol does not need a breaking change when signing is introduced.
21. As an operator, I want the Phase 0 agent installed on at least one real production Mac at a client site to behave correctly under actual network conditions, so that the architecture is validated against reality and not lab assumptions.
22. As an operator, I want the test deployment to target a low-stakes service (one whose unexpected restart will not disrupt critical operations), so that the experiment is safe to run on a real production device.
23. As an edge device, I want the agent to detect a failed `service.restart` (non-zero exit from `launchctl` / `systemctl`) and surface that as a failed command result, so that operators are not told a restart succeeded when it did not.
24. As a developer, I want unit and fake-transport integration tests for `mqtt-transport` covering connection lifecycle, reconnect-on-disconnect, and topic routing, so that the transport is verified without requiring AWS in CI.
25. As a developer, I want unit tests for `command-dispatcher` covering handler dispatch, unknown-command rejection, handler-error response formatting, and `correlation_id` propagation, so that the dispatcher's behaviour is exhaustively verified.
26. As a developer, I want unit tests for `service-backend` via fakes plus per-OS smoke tests run on real hosts during deployment, so that the interface contract is verified and per-OS behaviour is confirmed end-to-end.
27. As a developer, I want CI to fail the build if any unit or fake-transport integration tests fail, so that the test policy (ADR-012) is operative from the first commit.
28. As a developer, I want the CI build matrix to cross-compile for all three target platforms and run tests for each, so that platform-specific build breakage is caught before merge rather than at deployment.
29. As a developer, I want the agent binary to have minimal runtime dependencies (statically linked Go binary, no C libs, no Python venv) so that it drops onto a Pi or Mac without installing additional packages (per ADR-002 rationale).
30. As a developer, I want the Phase 0 work to populate the `Verification` field of any ADRs whose implementation it begins (notably ADR-002, ADR-011, ADR-012), so that the ADR-template convention (`docs/agents/adr-template.md`) is honoured from the first phase.

## Implementation Decisions

**Language and build (per ADR-002).** Go. Single codebase. Cross-compile matrix targets `darwin/arm64`, `darwin/amd64`, `linux/arm64`. Service backend abstraction via Go build tags (`//go:build darwin` and `//go:build linux`).

**Module decomposition.** Five modules, sketched and confirmed:

- **`mqtt-transport`** — mTLS connection management to IoT Core; auto-reconnect with exponential backoff; subscribe/publish API. Inputs: `{certPath, keyPath, brokerURL, deviceID}`. Outputs: a `Transport` value exposing `Subscribe(topic, handler) error` and `Publish(topic, payload []byte) error`. Owns no business logic; bytes in, bytes out.
- **`command-dispatcher`** — Receives raw MQTT messages on `devices/{id}/cmd`, decodes the JSON envelope, looks up a handler in a registry by command `type`, executes, publishes the result envelope on `devices/{id}/cmd-result`. Inputs: a `Transport` and a `map[string]Handler` registry. Wraps handler errors uniformly. Propagates `correlation_id` from request to response.
- **`service-backend`** — OS abstraction with two methods: `Status(name string) (State, error)` and `Restart(name string) error`. macOS implementation shells out to `launchctl`; Linux implementation shells out to `systemctl`. Build-tag separated; the public interface is identical.
- **`telemetry-publisher`** — Periodic heartbeat publisher. Inputs: `{interval, collectors []func() map[string]any, transport}`. On each tick, runs all collectors, merges results into a single payload, publishes on `devices/{id}/telemetry`. Collector errors are logged and skipped — they do not crash the publisher.
- **`agent-cli`** *(developer-laptop tool, not the agent)* — Reads developer credentials from a local config, connects to IoT Core, publishes a chosen command type + args to a chosen device id, subscribes to the matching cmd-result topic, prints the response. Throwaway harness; replaced by the Phase 1 CP API.

**MQTT topics (per `architecture.md`).** `devices/{id}/cmd` (CP → device), `devices/{id}/cmd-result` (device → CP), `devices/{id}/telemetry` (device → CP). The device shadow topics (`$aws/things/{id}/shadow/...`) are not used in Phase 0 — shadow integration lands when desired/reported state-tracking is implemented.

**Command envelope schema (JSON).** Reserved fields:

```
{
  "correlation_id": "<UUIDv7>",      // required
  "command_id":     "<UUIDv7>",      // required
  "type":           "<string>",       // required, e.g. "heartbeat", "service.status"
  "args":           { ... },          // optional, command-specific
  "issued_at":      "<RFC 3339>",     // required
  "expires_at":     "<RFC 3339>",     // optional
  "signature":      null              // reserved; populated by Phase 3 Ed25519 signing
}
```

**Result envelope schema (JSON).** Reserved fields:

```
{
  "correlation_id": "<echo of request>",
  "command_id":     "<echo of request>",
  "success":        true | false,
  "result":         { ... } | null,    // present when success
  "error":          { "code": "...", "message": "..." } | null,  // present when !success
  "started_at":     "<RFC 3339>",
  "finished_at":    "<RFC 3339>"
}
```

**Logging (per ADR-011).** `log/slog` with the JSON handler. Required fields per line: `ts`, `level`, `service`, `correlation_id`, `msg`. The `correlation_id` is established at command intake (from the envelope), propagated through every log call in the handler, and echoed in the result envelope.

**mTLS provisioning.** Manual for Phase 0. Provisioning runbook is part of this deliverable. The IoT Core CA is created once; per-device certs are issued by the CA and copied to each device alongside the agent binary. The Phase 1 `POST /enrollments` endpoint replaces this manual step (per ADR-004); the runbook will be retired when Phase 1 lands.

**Agent installation.** A LaunchDaemon plist on macOS and a systemd unit on Linux. Both register the agent to start on boot and restart on crash with a brief backoff. Phase 0 ships these as static files; Phase 1's `mac-mini-rollout/modules/11-cp-agent.sh` will template and install them.

**MQTT library.** Choose between `eclipse/paho.mqtt.golang` and `eclipse/paho.golang` at implementation time based on test-broker support quality. Either is acceptable; the choice is reversible.

**Config delivery.** Single config file at a well-known path (e.g. `/usr/local/etc/uknomi-agent/config.json` on macOS; `/etc/uknomi-agent/config.json` on Linux), readable by the agent's runtime user. Env-var overrides are supported for development.

**No Ed25519 signing in Phase 0.** The protocol reserves the `signature` field but does not populate or verify it. Signing arrives in Phase 3 per the roadmap. This is the correct staging: building signing now without the CP API to issue signed commands would be premature.

**No CP API in Phase 0.** The `agent-cli` is the only thing that talks to IoT Core from the CP side. It uses long-lived developer IAM credentials and is not exposed to anyone but the implementing engineer.

**Field deployment.** At least one Mac Mini at a real client site (the specific site is deferred to deployment time; pick a low-stakes one — see User Story 22). At least one Linux device (Pi or Radxa, lab or a quiet client site — exact location matters less since Linux is deprecating per ADR-007).

## Testing Decisions

**What makes a good test.** Tests assert external behaviour, not implementation details. A test that says "the dispatcher calls `handler.Run()`" is bad — it locks the implementation in place. A test that says "publishing `{type: 'heartbeat'}` produces a cmd-result with `success: true`, a non-empty `result.device_id`, and the same `correlation_id` as the request" is good — it asserts what an outside observer can see, leaving the implementation free to evolve.

**Per-module test plan:**

- **`mqtt-transport` — heavy.** Unit tests for the connection-config loader and topic-routing logic. Fake-broker integration tests for: connection lifecycle (connect, subscribe, receive, publish, disconnect), automatic reconnect after a deliberately-cut connection, message delivery to the correct subscriber when multiple topics are subscribed. The test broker is either the Paho in-process test mode or a containerised Mosquitto via `testcontainers-go`. CI must not require AWS.
- **`command-dispatcher` — heavy.** Pure unit tests with a fake transport and a fake handler registry. Cases: known-command dispatch returns success envelope; unknown-command returns failure envelope with a stable error code; handler panic is caught and surfaces as a failure envelope (not a process crash); `correlation_id` from the request appears unchanged in the response; malformed JSON envelope is rejected without crashing the dispatcher.
- **`service-backend` — heavy on interface, smoke-only on impl.** The interface is tested via a fake that the dispatcher uses for unit tests. The macOS `launchctl` implementation is smoke-tested on a real Mac during Phase 0 deployment (one `service.status` of a known launchd job; one `service.restart` of a chosen low-stakes service). The Linux `systemctl` implementation gets the equivalent smoke test on the Linux device. Unit-mocking shell invocations of `launchctl` / `systemctl` is explicitly avoided — it tests nothing useful.
- **`telemetry-publisher` — smoke.** One test that asserts an interval ticks produce a publish; one test that asserts a collector error is logged and the next tick still fires (resilience). No exhaustive coverage — it is orchestration code that will evolve as telemetry grows.
- **`agent-cli` — smoke.** It is a throwaway developer harness. One happy-path test that asserts publishing a `heartbeat` command yields a printed response. Nothing more.

**Property-based test recommendation (not mandatory).** The command-envelope and result-envelope JSON schemas are good candidates for a serialize/deserialize roundtrip property test. Cheap to write; catches accidental schema drift early; pays dividends in Phase 3 when the `signature` field starts being populated.

**CI gate (per ADR-012).** All unit and fake-transport integration tests must pass for the PR to be eligible to merge. The CI matrix cross-compiles for all three target platforms. CI does not exercise real IoT Core — the cost-benefit is wrong for Phase 0; LocalStack IoT Core integration is a Phase 1 evaluation.

**Prior art.** None — this is the first implementation work in the repo. The patterns established here will be referenced by Phase 1 modules. ADR Verification fields (currently `TBD — added at implementation`) for ADR-002, ADR-011, and ADR-012 should be populated as part of this phase with the test paths created here.

## Out of Scope

The following are explicitly **not** part of Phase 0 and must not be built as part of this PRD:

- Control Plane API service (Phase 1)
- Dashboard / Next.js UI (Phase 1)
- AWS infrastructure as Terraform/CDK — the IoT Core CA and the one or two things for Phase 0 are provisioned manually in the console (Phase 1 codifies infra)
- Enrollment endpoint and bootstrap-token flow (Phase 1 per ADR-004; ADR-014)
- Ed25519 signing and signature verification (Phase 3)
- `reboot` and `run-script` commands (Phase 3)
- Audit log (Phase 3)
- Idempotency-key enforcement (Phase 3, with mutating endpoints)
- Agent self-update + auto-rollback (Phase 3 per ADR-013)
- Cert rotation (Phase 4)
- Edge UI proxy via Tailscale subnet router (Phase 2)
- Log tail endpoint (Phase 2)
- WebSocket live updates (Phase 3)
- Multi-device rollout (Phase 1)
- LocalStack IoT Core integration in CI (Phase 1 evaluation)
- Distributed tracing (deferred per ADR-011)
- Push notifications, mobile app work (post-Phase 4)
- Removing or modifying `mac-mini-rollout` install modules other than adding Phase 0 docs (the new `11-cp-agent.sh` is a Phase 1 deliverable)

## Further Notes

**Success criteria (per `docs/roadmap.md` Phase 0).** Commands round-trip in under 2 seconds when the device is online. The agent reconnects automatically after a network interruption. The identical binary works on Mac and Linux with no platform-specific patches required.

**Primary risk.** Client-site firewalls may block MQTT-over-WSS (outbound HTTPS on 443). The deployment to the first client site is the critical experiment. If it fails, the Phase 0 result is "the architecture does not work as designed under real client-site network conditions" — which means revisiting ADR-001 (IoT Core) or pursuing the rejected Tailscale-pulled command channel as a fallback. This is exactly why Phase 0 exists: to surface this risk cheaply.

**Forward-compatible decisions baked in now.** Reserved `signature` field in the command envelope (for Phase 3 signing). `correlation_id` mandatory in every envelope and every log line (for ADR-011's end-to-end correlation). Command handler registry pattern (for adding `reboot`, `run-script`, `service.start/stop`, `agent.update` in Phase 3). Build-tag separation (for any future per-OS feature without restructuring).

**ADR Verification follow-ups.** This phase begins implementation of ADR-002 (Go agent), ADR-011 (structured logs + correlation IDs), and ADR-012 (test policy + CI gate). The `Verification` fields of those ADRs (currently `TBD — added at implementation`) should be updated as part of this work to point at the concrete test paths and CI configuration created here. See `docs/agents/adr-template.md`.

**Open question for deployment.** Which client site hosts the Mac Mini for the field validation? Pick at deployment time. Criteria: low-stakes (a service restart will not disrupt critical operations), reachable for a remote-validate cycle (operator can confirm the device is behaving via a phone call if needed), and ideally one site that is representative of the typical client-site NAT and firewall posture.
