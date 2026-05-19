# ADR-011: Structured JSON logs with end-to-end correlation IDs

**Status:** Accepted (2026-05-18)

**Context.** The signed-command flow crosses five processes: browser → API → KMS → IoT Core → agent → IoT Core → ingest worker → API → browser (via WebSocket). Under AFK-agent dev, debugging incidents requires correlating events across these boundaries from log search alone. Unstructured logs make this impractical; the cost of retrofitting structured logging across an existing codebase is high.

**Decision.**

- All Go services log via `log/slog` with JSON output. Required fields per line: `ts`, `level`, `service`, `correlation_id`, `msg`. Common optional fields: `device_id`, `command_id`, `user_id`.
- A `correlation_id` is generated at the API request boundary (UUIDv7) and propagated through:
  - Postgres writes (column on `commands`, on `audit_log`)
  - KMS sign call (request id metadata)
  - **The signed command payload** — `correlation_id` is a required field of the protocol
  - The agent's log when processing the command
  - The `cmd-result` reply payload
  - The ingest worker's processing
  - The WebSocket event to the dashboard
- Log query surface: CloudWatch Logs Insights. No distributed tracing system initially (deferred until log-correlation proves insufficient).
- Retention: 30 days for app logs in CloudWatch. Audit log lives in Postgres + S3 (this ADR scope is app logs).
- Log levels: standard `DEBUG/INFO/WARN/ERROR`. `INFO` baseline in production. `DEBUG` enabled per-service via an env var without redeploy.

**Consequences.**
- (+) Multi-service debugging is tractable — one `correlation_id` returns every log line for an entire command lifecycle.
- (+) Cheap operationally — no vendor cost, no additional services. CloudWatch is already in the stack.
- (+) `correlation_id` in the signed payload means the agent's per-device logs are queryable by the same id used at the API.
- (-) `correlation_id` is now part of the signed command protocol. Protocol changes are breaking.
- (-) No distributed tracing means latency analysis across hops requires manual log inspection. Acceptable until pain emerges.

**Verification.** TBD — added at implementation. The signed-command schema test asserts `correlation_id` is required. A CI lint enforces that every log call carries a `correlation_id` field where request context is available.
