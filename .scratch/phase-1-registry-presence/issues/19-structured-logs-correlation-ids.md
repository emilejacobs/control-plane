# Issue 19 — Structured logs + correlation ID library

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 31, 33, § Implementation Decisions.
- ADR: ADR-011 (structured JSON logs with end-to-end correlation IDs).

## What to build

The shared logging library that all CP Go services use, plus the lint/CI mechanism that enforces correlation-ID propagation through every request. Lands early so subsequent slices use it from the first commit.

Scope:

- `internal/cp/log` (or equivalent) package: thin wrapper around `log/slog`. JSON handler at INFO by default; standard fields (`service`, `version`, `correlation_id`, `device_id`, `operator_id`, `request_id`) included where available via `slog.With`.
- HTTP middleware that:
  - Extracts `X-Correlation-Id` from inbound requests (or generates a new UUID if absent).
  - Stuffs it into request context.
  - Adds it to the response header.
  - Wraps the request's `slog.Logger` so every log line carries it.
- For the ingest worker: every SQS message handler reads `correlation_id` from the message envelope (per the ADR-011 schema requirement) and stuffs it into a per-handler context.
- For outbound IoT publishes (signed-command payloads, future phases): the publish helper propagates `correlation_id` into the message envelope.
- Lint rule or test that fails CI if any handler logs without going through the context-bound logger (i.e., direct `slog.Info` calls without the context wrapper).

## Acceptance criteria

- [ ] All existing log lines in `cp-api` and `cp-ingest` emit JSON with the standard fields.
- [ ] A `POST /enrollments` call propagates its `X-Correlation-Id` through to: the API service's log, the ingest worker's heartbeat log for the same device (once it heartbeats post-enrollment), and the audit log entry.
- [ ] An integration test confirms end-to-end correlation: send a request with a known `X-Correlation-Id`, query CloudWatch (or test log sink) for all matching lines across services.
- [ ] CI check fails on a deliberately-broken handler that logs without the context wrapper.

## Blocked by

- None — lands early so subsequent slices benefit. Can run in parallel with #03.
