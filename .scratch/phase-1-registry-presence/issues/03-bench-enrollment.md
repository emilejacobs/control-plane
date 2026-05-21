# Issue 03 — Bench enrollment end-to-end

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § Solution, § User Stories 1–4, 34, § Implementation Decisions (Registry, IoTProvisioner modules; enrollment schema; API contracts).
- ADRs: ADR-004 (install-script enrollment), ADR-009 (Go API service), ADR-012 (idempotency), ADR-015 (Postgres multi-AZ), ADR-017 (static bootstrap key — used here in dev-mode env-var form; production Secrets Manager wiring lands in #10).

## What to build

The first vertical slice of the Control Plane API: a `POST /enrollments` call mints an IoT Core thing + per-device mTLS cert + Postgres `devices` row, idempotent by hardware UUID; `GET /devices/{id}` returns the row. Demoable end-to-end via curl from an engineer's laptop against a Terraform-provisioned dev environment.

Scope:

- Postgres schema for `devices` (per PRD schema sketch) and the migration that creates it. Tooling settled in #02.
- HTTP API skeleton: router, JSON encoding, panic recovery, request logging.
- Idempotency middleware (per ADR-012): rejects state-mutating requests without `Idempotency-Key`; stores key + canonical response in `enrollment_idempotency` table; replays return the stored response.
- `IoTProvisioner` module: wraps AWS IoT SDK; `ProvisionDevice(deviceID) → (ThingARN, CertPEM, PrivKeyPEM, CertARN, CertExpiresAt)`, `Revoke(CertARN)`. Testable against LocalStack or `moto`.
- `Registry` module: `Enroll(BootstrapKey, Hardware, Hostname) → Device + Cert`. Encapsulates idempotency + IoT provisioner + Postgres insert behind one interface. The bootstrap key is loaded from an env var for this slice (`CP_BOOTSTRAP_KEY`); production Secrets Manager wiring is #10.
- `POST /enrollments`: accepts `{bootstrap_key, hostname, hardware_uuid, hardware_kind, os_version, agent_version}`, returns `{device_id, mtls_cert_pem, mtls_private_key_pem, iot_endpoint, iot_thing_arn, mtls_cert_expires_at}`.
- `GET /devices/{id}`: returns the row, unauthenticated for this slice (auth lands in #04). Marked clearly as dev-only behind a feature flag so the next slice can flip it on.

No auth, no presence, no rate limiting, no hardening — those are subsequent slices. The slice exists to prove the enrollment path works end-to-end through every layer.

## Acceptance criteria

- [ ] `POST /enrollments` against a dev environment with the bootstrap-key env var set mints an IoT thing + cert and inserts a `devices` row.
- [ ] A second `POST /enrollments` with the same `Idempotency-Key` returns the original response without re-creating the row or minting a new cert.
- [ ] `GET /devices/{id}` returns the inserted row.
- [ ] Integration test exercises the full flow against a Postgres test container and a LocalStack or `moto` IoT endpoint.
- [ ] Idempotency CI gate test (per ADR-012) is in place and passes — any future state-mutating endpoint added without `Idempotency-Key` enforcement will fail it.
- [ ] An agent installed with the returned cert can connect to IoT Core and publish to its `devices/{id}/telemetry` topic (verifies the policy + cert are correct — uses the agent-cli pattern from Phase 0).

## Blocked by

- Issue 02 (schema migrations tooling decision).

## Comments

### 2026-05-21 — landed in 10 commits (`f4df496`..`8a7689d`)

AFK acceptance criteria all green. Test posture: 8 integration tests
behind testcontainers Postgres + moto IoT, ~25s total runtime.

- `POST /enrollments` mints IoT thing + cert + Postgres row — verified
  with the fake provisioner (cycle 1) and against moto end-to-end
  (cycle 9, `TestEnrollmentAgainstMotoIoT`).
- Idempotent replay with the same `Idempotency-Key` returns the stored
  body and does not re-mint a cert (cycle 3,
  `TestEnrollmentIdempotentReplay`). Replay returns 200 per PRD; first
  is 201.
- `GET /devices/{id}` returns the inserted row, gated behind the
  `CP_DEV_DEVICES_GET` env var that #04 will remove (cycle 2).
- Idempotency CI gate: `api.Builder` records every state-mutating
  route, and `TestIdempotencyGate` probes each one without the header
  to confirm 400 (cycle 7). A new `POST/PUT/PATCH/DELETE` registered
  through `Builder` is wrapped by construction; bypassing `Builder`
  requires editing `internal/cp/api/api.go` directly.
- Error semantics: bad bootstrap key → 401 (cycle 5), missing
  Idempotency-Key → 400 (cycle 4), unknown device → 404 (cycle 6),
  with the 4xx-not-stored property pinned by the cycle 5 test.
- `cmd/cp-api/main.go` (`8a7689d`) wires the binary: pgxpool +
  on-startup goose, IoT client (`AWS_ENDPOINT_URL`-aware), graceful
  shutdown on SIGTERM, listens on `PORT` (default 8080).

**Pivot worth noting:** ADR-012 and Issue 03 said "LocalStack or
moto" for IoT. Cycle 8 tried LocalStack first; LocalStack Community
returned 501 (IoT is a Pro feature). Switched to `motoserver/moto:latest`.
Stored in memory so future agents don't re-discover. Other AWS
services (S3/KMS/Secrets/SQS) can still use LocalStack.

**Open / HITL:** acceptance criterion "an agent installed with the
returned cert can connect to IoT Core and publish to its topic" — that
is the Wave 0 bench smoke (Issue 12), explicitly out of scope for AFK.
The plumbing is ready for that smoke to run against real AWS once Issue
01's Terraform is `apply`'d.
