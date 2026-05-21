# PRD: Phase 1 — Registry, presence, and enrollment

Status: ready-for-agent
Phase: 1 (per `docs/roadmap.md`)
Created: 2026-05-21
Source: distilled from the 2026-05-21 grilling session captured in this directory's `README.md` decisions log.

## Problem Statement

As the uKnomi operations team, we manage 63 edge devices across multiple US client sites via a spreadsheet (`uknomi-macmini-devices.xlsx`) and ad-hoc SSH/Tailscale access. The spreadsheet is stale by definition — it records what *should* exist, not what *is* working right now. When an operator needs to know whether a Mac Mini at a client site is online, they SSH to it or ask the client to call back; both are slow, unreliable, and don't scale beyond a handful of devices. Phase 0 proved the command channel works end-to-end through real client-site NAT. We are now ready to replace the spreadsheet with a live, queryable registry — but only for what's safely scoped to a read-only first delivery.

## Solution

Build the Control Plane registry and presence dashboard:

- **AWS infrastructure in Terraform** for: VPC, ALB, RDS Postgres (multi-AZ per ADR-015), Fargate cluster, IoT Core CA, S3 buckets, KMS keys, Tailscale subnet router task, SQS queues (presence-heartbeat + presence-lifecycle, each with DLQ).
- **`uknomi-control-plane-api` Fargate service** (Go, per ADR-009): authentication (local credentials + TOTP per ADR-010), enrollment endpoint, device list/detail endpoints. No WebSocket — polling-only in Phase 1.
- **`cp-ingest` Fargate worker** (Go, per ADR-018): SQS consumer for IoT-Rule-routed heartbeats and lifecycle events; sweeper goroutine for staleness-driven offline transitions.
- **Next.js dashboard:** login (with forced TOTP enrollment), fleet view (online/offline grouped by client/site), per-device view (static fields + presence + mTLS cert expiry). All live data through TanStack Query.
- **`mac-mini-rollout/modules/11-cp-agent.sh`:** the install module that runs at Mac provisioning time, presents the static bootstrap key (per ADR-017), calls `POST /enrollments`, installs the agent's mTLS cert, registers the agent as a LaunchDaemon, and starts it.
- **One-page Linux install script:** the equivalent for Pi/Radxa. Built in Phase 1; rolled out as a parallel track per the wave structure below.
- **Wave-based rollout to ~25 Macs** (Linux rollout deferred to a parallel track), replacing the spreadsheet for the Mac portion of the fleet at the retirement gate.

Out of scope explicitly: commands (Phase 3), service-status reporting (Phase 2), Edge UI proxy (Phase 2), camera snapshots (Phase 2), telemetry charts (Phase 2), live WebSocket updates (Phase 2), cert rotation (Phase 4), agent self-update (Phase 3).

## User Stories

Actors:
- **Operator (staff)** — uKnomi internal user with full fleet access in Phase 1.
- **Operator (local)** — future field operator with site-scoped access (mentioned only where Phase 1 has to prepare the ground).
- **Engineer** — the developer rolling out and operating the system; also covers the AI-agent-as-developer model per `MEMORY.md`.
- **Edge device** — a Mac Mini at a client site running the agent.
- **Architectural reviewer** — the human reviewing AI-agent contributions.

### Enrollment

1. As an engineer, I want a single static bootstrap key bundled into the `mac-mini-rollout` install package (fetched from AWS Secrets Manager at CI build time) so that no per-device manual step is required during a Mac install (per ADR-017).
2. As an engineer, I want the install module to call `POST /enrollments` with hardware UUID, hostname, and the bootstrap key, so that the device is registered in the CP and receives a per-device mTLS cert in one step.
3. As an engineer, I want `POST /enrollments` to be idempotent by hardware UUID (Idempotency-Key per ADR-012 and the architecture's `Idempotency-Key: <hardware_uuid>` convention), so that a retry from a flaky cellular link at a client site does not produce a duplicate device record.
4. As an engineer, I want the enrollment endpoint to mint an IoT Core thing + per-device cert during the call and return the cert + private key in the response (one-time fetch), so that the install script can install the cert on the device with no further round-trips.
5. As an engineer, I want enrollment failures to be visible in the audit log with source IP, hardware UUID, hostname, and the failure reason, so that I can diagnose install-time issues without SSHing.
6. As a security-minded reviewer, I want the enrollment endpoint to enforce a per-source-IP rate limit (20 req/hour) and to alert when hostnames don't match the project naming convention regex, so that a compromised bootstrap key has bounded blast radius (per ADR-017).
7. As an engineer, I want the bootstrap key to be rotatable by updating Secrets Manager and rebuilding install packages (cadence ~6 months), so that key compromise can be remediated without re-architecting the enrollment path.

### Presence

8. As an operator (staff), I want every enrolled device to publish a heartbeat to `devices/{id}/telemetry` every 30 seconds, so that the CP has a reliable freshness signal independent of TCP connection state.
9. As an operator, I want a device to count as "online" if its `last_seen` is within 90 seconds (3× heartbeat interval, tolerating one missed publish), so that the dashboard surfaces real outages quickly without flapping on a single missed heartbeat.
10. As an operator, I want the CP to react to IoT Core `connected` / `disconnected` lifecycle events as the fast-path for the online → offline transition, so that I see a device go offline within seconds rather than waiting up to 90 seconds for sweeper detection.
11. As an operator, I want a background sweeper to mark stale devices offline on a 30-second tick, so that devices that lose connectivity without sending a TCP FIN (NAT timeout, power yank) appear offline within at most 60 seconds even when IoT Core's lifecycle event doesn't fire.
12. As an operator, I want the fleet view to refresh every 10 seconds via polling, so that presence transitions are visible within at most 10 seconds without the operator manually refreshing.

### Dashboard

13. As an operator (staff), I want a login page that accepts username + password + TOTP code, so that I can authenticate with no external IdP and the strong factor enforced (per ADR-010).
14. As the first engineer to deploy the CP, I want a `/auth/first-run` endpoint that creates the very first admin account if and only if no users exist, so that bootstrap requires no `psql` runbook and no infra-driven seed.
15. As an operator, I want to be forced into TOTP enrollment + password change on first login, so that no account exists in a half-bootstrapped state.
16. As an operator, I want 10 single-use recovery codes issued at TOTP enrollment time (shown once, stored hashed), so that I can recover access if my TOTP device is lost.
17. As an operator, I want a per-account lockout (5 failed attempts → 15 min) and a per-IP rate limit on `/auth/login`, so that account enumeration and brute-force attempts are bounded.
18. As an operator, I want JWT bearer tokens valid for 1 hour with refresh tokens valid for 24 hours (refresh tokens hashed in Postgres, revocable), so that long-running sessions don't expose long-lived credentials.
19. As an operator, I want a fleet view that lists all devices I'm authorized to see, grouped by client and site, with online/offline state visible at a glance, so that I can replace the spreadsheet as my source of truth.
20. As an operator, I want a per-device view that shows: hostname, client/site, hardware kind, OS version, agent version, hardware UUID, IoT Thing ARN, mTLS cert expiry (with days-remaining), enrollment date, and `last_seen` (with ago-string), so that I have all the static context I need without SSHing.
21. As an architectural reviewer, I want the mTLS cert expiry to be visible on the per-device view from Phase 1 (even though rotation lands in Phase 4 per ADR-013), so that we have an early-warning signal if Phase 4 slips past month 10 and certs are approaching expiry.
22. As an engineer maintaining the dashboard, I want all live data to flow through TanStack Query (or equivalent) with no `setInterval` calls in components, so that the Phase 2 migration to WebSocket is additive (push deltas into the same cache via `setQueryData`) rather than a refactor.

### Authorization (groundwork for future field operators)

23. As an architectural reviewer, I want every device-touching handler to call a `scopedDeviceQuery(ctx, baseQuery)` helper that filters by the operator's site allowlist, so that when local operators arrive in a later phase, no Phase 1 endpoint silently returns more than the operator should see.
24. As an engineer, I want all Phase 1 staff accounts to have a `'*'` site allowlist (or an equivalent staff flag) granting full fleet access, so that the enforcement machinery is exercised from day one but does not constrain Phase 1 operators.
25. As an architectural reviewer, I want a CI integration test that fails when any device-touching handler returns data without going through `scopedDeviceQuery`, so that the structural rule is enforced rather than aspirational (same posture as ADR-012's idempotency CI gate).

### Rollout (waves)

26. As an engineer, I want to bring up the bench device (Wave 0, reusing the Phase 0 Mac after decommissioning the Phase 0 thing + cert) to validate the codified Terraform + the new install module end-to-end before touching any client device.
27. As an engineer, I want to roll out to a single pilot client site (Wave 1, ~3–5 devices) and watch the dashboard with one operator for one week with no manual intervention before bulk rolling out.
28. As an engineer, I want to roll out to the remaining ~25 Macs (Wave 2) only after Wave 1 is stable, so that bulk rollout cost is paid against validated tooling.
29. As an engineer, I want Wave 3 (Linux: 36 Pis + 2 Radxas) to be a parallel track that does **not** gate Phase 1's exit, so that Linux rollout effort doesn't bottleneck the Mac value delivery (per ADR-007 and the Mac-consolidation direction in `MEMORY.md`).
30. As an engineer, I want the Phase 0 `dev-mac-mini-emile` device fully decommissioned (cert revoked, thing deleted, agent uninstalled) before Wave 0, so that Wave 0 exercises the install module on a clean machine — which is its purpose.

### Operability

31. As an engineer, I want every ingest message validated against a schema with a required `correlation_id` (per ADR-011), so that any log line in the chain (agent → IoT Core → SQS → ingester → Postgres) can be traced back to a single event.
32. As an engineer, I want the ingest worker to send malformed payloads to a DLQ (not crash) and to page when DLQ depth > 0, so that bad messages don't take down the ingest path silently.
33. As an engineer, I want all CP services and the ingest worker to emit structured JSON logs via `log/slog` (per ADR-011), so that observability tooling can index by `device_id`, `operator_id`, `correlation_id`, and `request_id` uniformly.
34. As an architectural reviewer, I want the enrollment endpoint and every other state-mutating endpoint to accept an `Idempotency-Key` header (per ADR-012), so that retries from flaky links are safe and the Phase 1 CI gate catches any endpoint that forgets.

### Ship and retirement gates

35. As the team, we want a **ship gate**: all ~25 Macs enrolled in Waves 0–2; presence accurate within 60s; dashboard groups devices by client/site; login + TOTP works for the Phase 1 operator set — verifiable in a single sitting.
36. As the team, we want a **retirement gate**: Mac rows are removed from the spreadsheet on a team-picked calendar date roughly two weeks after the ship gate; Linux rows get an "enroll via dashboard once ready" banner and stay until Wave 3 completes (parallel track). We do not measure operator usage habit — we remove the alternative for the Mac portion.

## Implementation Decisions

### Architectural decisions made during the 2026-05-21 grilling

- **ADR-017** ratified: static bootstrap key bundled in install package (Secrets Manager → CI → package). Supersedes ADR-014. Hardening: rate limit + hostname-convention anomaly alert + per-request audit log. Blast radius bounded by architecture (per-device mTLS certs minted at enrollment, not granted by the bootstrap key).
- **ADR-018** ratified: Fargate workers (not Lambda) for all MQTT-side ingest, all phases. IoT Rule → SQS → Go consumer; sweeper as goroutine. Drivers: paradigm parsimony, AI-agent-development friendliness, persistent Postgres connection pool. Throughput math (~50–200 events/min across phases) makes Lambda's scaling case immaterial.
- **Two-gate exit criterion** (ship + retirement). No "operator usage" metric.
- **Four-wave rollout** (Bench → Pilot site → Mac fleet → Linux tail), with Wave 3 deferred out of Phase 1 exit.
- **Issues-led, PRD-emergent.** This PRD is the consolidation of the grilling session; future issues reference PRD sections rather than embedding their own "Decisions to make" blocks. Issue 01 has already been retrofitted to this rule.
- **Enrollment-first registry model.** A device row exists only after successful `POST /enrollments`. No spreadsheet seed step, no `expected` status. The spreadsheet retains its "what should exist" job through Phase 1 rollout; wave engineers check the dashboard against the spreadsheet to surface failures.
- **First-run admin** for the bootstrap account. No `psql` runbook, no infra-driven seed. Engineer claims the account immediately on first deploy. Reviewer chose this over the runbook explicitly; ADR was declined.
- **Polling, not WebSocket, in Phase 1.** 10s poll cadence; all live data flows through TanStack Query (or equivalent). Structural rule: no `setInterval` in components.
- **Site-scoped authorization enforced from day one** via `scopedDeviceQuery` helper + CI test. All Phase 1 operators are staff with `'*'` allowlist.
- **Phase 0 device decommissioned, not migrated.** Wave 0 reuses the hardware against fresh Terraform-managed resources.

### Module decomposition

Deep modules (simple interface, dense internal mechanism, fake-clock-able):

- **`AuthN`** — owns Argon2id password hashing, TOTP enroll/verify, recovery code issuance and consumption, first-run-admin lifecycle, JWT issuance and refresh, account lockout. Interface: `Login`, `Refresh`, `EnrollTotp`, `VerifyTotp`, `ClaimFirstRunAdmin`.
- **`AuthZ`** — owns the operator site allowlist resolution and the `scopedDeviceQuery` helper. A middleware injects the operator's allowlist into the request context; the helper composes the filter into any device-touching query. Staff `'*'` allowlist is the single branch.
- **`Registry`** — owns the enrollment-first device lifecycle. Interface: `Enroll(BootstrapKey, Hardware, Hostname) → Device + Cert + PrivKey`, `GetByID`, `List(filter)`, `UpdateLastSeen`. Idempotency by hardware UUID. Bootstrap-key validation, IoT-Core thing+cert minting, and Postgres write happen behind this interface so callers never see AWS or DB primitives.
- **`Presence`** — owns the 90-second freshness threshold, the sweep tick, the connect/disconnect fast-path. Interface: `RecordHeartbeat(deviceID, ts)`, `OnConnect(deviceID)`, `OnDisconnect(deviceID)`, `Sweep() → []TransitionedDevice`. Fake-clock-able. All Q2 logic centralized.
- **`SQSConsumer[T]`** — generic SQS consumer with schema validation (correlation_id required), DLQ posture, structured logging, graceful shutdown. Call site: `NewSQSConsumer(queue, handler).Run(ctx)`. Reused by Phase 1 presence/lifecycle ingesters and by every later-phase ingest concern.

Moderately deep:

- **`IoTProvisioner`** — wraps AWS IoT SDK behind `ProvisionDevice → (ThingARN, CertPEM, PrivKey, CertARN)` and `Revoke(CertARN)`. Keeps `Registry.Enroll` AWS-knowledge-free.

Shallow:

- **`PresenceIngester`** / **`LifecycleIngester`** — small handlers wired into `SQSConsumer[T]`; both delegate to `Presence`.
- **`PresenceSweeper`** — goroutine wrapper around `Presence.Sweep()` with `time.NewTicker(30*time.Second)`.

Dashboard:

- **`api/queryClient`** (TS) — TanStack Query client with bearer-token interceptor; exposes `useDevices`, `useDevice`, `useLogin`. The "no setInterval in components" rule is enforceable by virtue of components only depending on this surface.

Infra (Terraform):

- **`modules/device`** — per-device IoT thing + cert (started in Issue 01).
- **`modules/iot-policy`** — the corrected policy shape from Phase 0 Issue 10.
- **`modules/sqs-ingest`** — reusable SQS + DLQ + IoT-Rule wiring per ingest topic.

### Schema decisions

- **`devices`**: enrollment-first (no `expected` status). Columns: `id uuid`, `hostname text`, `client_id uuid`, `site_id uuid`, `hardware_kind enum`, `hardware_uuid text`, `mtls_cert_arn text`, `last_seen timestamptz`, `iot_thing_arn text`, `agent_version text`, `os_version text`, `enrolled_at timestamptz`, `created_at`, `updated_at`. Unique constraint on `hardware_uuid`.
- **`operators`**: `id uuid`, `email text unique`, `password_hash text` (Argon2id), `totp_secret_encrypted bytea`, `recovery_codes_hashed text[]`, `is_staff bool`, `created_at`, `last_login_at`.
- **`operator_sites`**: `operator_id uuid`, `site_id uuid` (or sentinel `'*'` for staff — exact representation TBD at implementation). Composite PK.
- **`clients`**, **`sites`**: minimal `id` + `name` to start; expand as needed.
- **`audit_log`**: append-only, with `id`, `at`, `actor_id`, `actor_type` (operator / agent / system), `action`, `resource_kind`, `resource_id`, `correlation_id`, `source_ip`, `user_agent`, `payload jsonb`, `outcome`. Daily S3 mirror per architecture § Security.
- **`enrollment_idempotency`**: keys observed for `POST /enrollments` with the canonical response, per ADR-012's pattern.
- **`refresh_tokens`**: `id`, `operator_id`, `token_hash`, `issued_at`, `expires_at`, `revoked_at nullable`.

### API contracts

- `POST /auth/first-run` — body `{ email, password }`. 201 if `system_initialized = false`; 410 Gone otherwise. Audit log records source IP + UA + email + outcome. After success the flag flips irreversibly.
- `POST /auth/login` — body `{ email, password, totp_code }`. Rate-limited per-IP at the ALB, per-account lockout in `AuthN`. Returns `{ access_token (1h), refresh_token (24h) }`.
- `POST /auth/refresh` — body `{ refresh_token }`. Validates hash, rotates token.
- `POST /auth/totp/enroll` — authenticated, first-login-only. Returns provisioning URI + recovery codes (shown once).
- `POST /enrollments` — headers `Idempotency-Key: <hardware_uuid>`, body `{ bootstrap_key, hostname, hardware_uuid, hardware_kind, os_version, agent_version }`. Returns `{ device_id, mtls_cert_pem, mtls_private_key_pem, iot_endpoint, iot_thing_arn, mtls_cert_expires_at }`. 401 on bad bootstrap key, 429 on rate-limit, 200 on idempotent replay, 201 on first success.
- `GET /devices` — list scoped by operator site allowlist. Polled by dashboard at 10s.
- `GET /devices/{id}` — single device with full static fields + computed `is_online` + `last_seen_ago_seconds` + `mtls_cert_days_remaining`.

### Hardening + observability minima

- Per-source-IP rate limit on `/enrollments` (20 req/hour) and `/auth/login` (60 req/min).
- Hostname-convention regex alert on `/enrollments` (regex pinned in code).
- Audit log captures every state-changing request with `correlation_id` threading through the response and downstream events.
- DLQ alarm: presence + lifecycle queues paged on depth > 0.
- Sweeper-lag alarm: paged if `Sweep()` hasn't run in > 60s.
- Login-failure spike alarm: paged on > 100 failures in 5 min.

Additional observability decisions (metrics dashboards, distributed tracing, alerting thresholds for non-critical signals, on-call posture) are **TBD** — to be settled in a follow-on grilling.

### Explicit TBDs (originally gaps from the 2026-05-21 grilling)

Status updated 2026-05-21 (later that day) — three of the five resolved via Issue 02's grilling session.

1. ~~**Schema migrations tooling.**~~ → **Resolved by ADR-019**: goose, embedded via `embed.FS`, on-startup with Postgres advisory-lock serialization.
2. ~~**CI/CD pipeline shape.**~~ → **Resolved by ADR-020**: trunk-based, prod + staging environments, manual promotion to prod (switching to auto after 10 clean), three Fargate containers, Terraform state in S3+DynamoDB, CI via GitHub OIDC.
3. **Linux "one-page" install script constraints.** "One page" is the stated aspiration in the roadmap; the actual constraint (LOC ceiling? single-file? no external runtime dependencies?) needs to be pinned so the issue that builds it has a target. **Still open.**
4. **Cost ceiling.** `docs/costs.md` exists; the changes in this PRD (Fargate ingest worker, SQS queues, Timestream deferred, plus ADR-020's staging environment and ADR-021's CloudWatch baseline) move numbers. A reconcile pass is needed so subsequent decisions have a budget. **Still open.**
5. **Issue sequencing.** → **Resolved**: vertical-slice breakdown in this directory's issues `02`–`23`, sequenced by dependency, indexed in [`README.md`](./README.md).

Additionally, the observability platform question that emerged during the 2026-05-21 grilling was resolved by **ADR-021**: all-CloudWatch SDK direct (no OpenTelemetry in Phase 1).

## Testing Decisions

### What makes a good test in this codebase

Tests should exercise the *external behavior* of a module — the contract a caller sees — not the internal mechanism. A `Presence` test should set up a fake clock, record heartbeats, advance the clock, and assert that `Sweep()` returns the right transitioned devices — not test that the sweeper goroutine ticks at exactly 30s. Per ADR-012, integration tests are mandatory for every endpoint and every state-mutating endpoint must have an idempotency test. CI gates merge.

### Modules with explicit test coverage in Phase 1

The five deep modules — these are where bugs hide and where uniform test posture pays back across phases:

1. **`AuthN`** — unit + integration. Unit tests cover Argon2id verification (against precomputed hashes), TOTP code validation under a fake clock (rejecting drift outside ±1 window), recovery-code single-use semantics (used codes return false even if presented again), lockout escalation and reset. Integration covers the full `/auth/login` → `/auth/refresh` flow against a Postgres test container.

2. **`AuthZ`** — unit. Cover: staff (`'*'` allowlist) sees all rows; site-scoped operator sees only their sites; absent allowlist returns empty (fail-closed); the `scopedDeviceQuery` helper composes correctly with arbitrary base queries. The CI gate referenced in user story 25 lives here: a test that scans all handler files and fails if any device-returning handler doesn't go through the helper (similar posture to ADR-012's idempotency-CI-gate test).

3. **`Registry`** — integration. Cover: `Enroll` is idempotent by hardware UUID (a retry with the same hardware UUID returns the existing row, not a new one); bootstrap key validation rejects unknown keys with 401; rate limit returns 429 on the 21st request in an hour; hostname-convention violation emits an audit alert event; on success, an IoT thing + cert are minted and the cert ARN is returned. The IoT-Core-touching cases run against `moto` or LocalStack; the rest run against Postgres test container.

4. **`Presence`** — unit with fake clock. Cover: `RecordHeartbeat` updates `last_seen`; `Sweep` returns devices with `last_seen > 90s ago` and only those; `OnDisconnect` flips immediately without waiting for sweeper; `OnConnect` doesn't conflict with a stale `last_seen` from before the disconnect; transitions are emitted exactly once per state change. No real time or real DB needed.

5. **`SQSConsumer[T]`** — unit with fake SQS client. Cover: malformed payload routes to DLQ (handler not called); schema-validation failure (missing `correlation_id`) routes to DLQ with audit log entry; handler panic doesn't kill the consumer (message visibility timeout expires, redelivery happens, then DLQ); graceful shutdown drains in-flight messages within timeout. The Phase 2/3 ingest paths reuse this module so its test surface is amortized.

### Integration tests at the endpoint layer (per ADR-012)

Every HTTP endpoint gets at least one integration test. Every state-mutating endpoint gets an additional idempotency test that the existing CI gate enforces. The Phase 1 endpoint set is small (`/auth/*`, `/enrollments`, `/devices`, `/devices/{id}`); the gate keeps it small.

### Prior art for the test patterns

- The `internal/agent` and `internal/handlers` test layout from Phase 0 establishes the unit/integration split; Phase 1 follows that pattern in the API service.
- ADR-012's idempotency-key CI gate is the structural-rule precedent we reuse for `scopedDeviceQuery` enforcement (user story 25).
- The Phase 0 `agent-cli` end-to-end smoke (publish → respond) is the prior art for end-to-end enrollment smoke at the end of Wave 0.

## Out of Scope

The following are explicitly not in Phase 1 and have separate phases or no current phase:

- **Linux rollout (Wave 3).** Built in Phase 1 (the one-page install script), but the actual Pi/Radxa enrollment runs as a parallel track that does not gate Phase 1 exit. Driver: Pi/Radxa are deprecating (ADR-007), Mac consolidation is the fleet direction.
- **Commands (any kind).** Service restart, run-script, reboot — all Phase 3.
- **Service-status reporting** (per-device list of services and their state). Phase 2.
- **Edge UI proxy and camera snapshots.** Phase 2.
- **Telemetry charts (CPU/mem/disk over time) and Timestream.** Phase 2.
- **WebSocket / live push.** Phase 2 — when command-results and live service-state genuinely need it.
- **Cert rotation.** Phase 4 (Phase 1 ships 1-year certs per ADR-013).
- **Agent self-update.** Phase 3 with auto-rollback (ADR-013).
- **Decommission action surfaced in dashboard.** Phase 3.
- **Mosyle reconciliation job** (decommission devices removed from Mosyle). Phase 4.
- **Mobile app.** Not in any current phase; ADR-005 ensures the API is shaped to accommodate it.
- **Local field-operator accounts.** Not in Phase 1; the `AuthZ` machinery is exercised with staff-only `'*'` allowlists so the structural rule is in place when local operators arrive.

## Further Notes

### Linux deferral remains an outstanding ADR candidate

The Linux-rollout-deferred-from-Phase-1-exit decision (user story 29) passes all three ADR criteria (hard to reverse, surprising without context — original roadmap said "all 63 devices," real trade-off against partial spreadsheet retirement). The grilling session paused before promoting it. Recommend writing ADR-019 (or whichever number is next) before Wave 3 runs as a parallel track, so that the decision is findable from `docs/decisions.md` rather than only in this PRD.

### Branches still to grill

This PRD captures the spine. The following branches were enumerated during the session but not grilled, and at least the first three need to be settled before issues that depend on them can be filed:

- Schema migrations tooling
- CI/CD pipeline shape
- Linux "one-page" install script constraints
- Cost reconcile against `docs/costs.md`
- Observability beyond logs (metrics dashboards, tracing, alerting non-critical signals)
- Issue sequencing for Phase 1

### Source material

- Grilling session transcript and decisions log: `.scratch/phase-1-registry-presence/README.md`.
- ADRs created in the session: `docs/adr/0017-static-bootstrap-key-in-install-package.md`, `docs/adr/0018-fargate-workers-for-ingest.md`.
- Updated artifacts: `docs/CONTEXT.md` (added Heartbeat, Online threshold, Presence, Recovery code, First-run admin), `docs/decisions.md` (index), `docs/architecture.md` (diagram), `docs/roadmap.md` (Phase 1 section), `docs/runbooks/phase-0-iot-core-provisioning.md` (Phase 0 device decommission banner).
