# Architecture

**Status:** Living document. Initial design 2026-05-05; kept current as Phase 1 lands — last updated 2026-05-21, reflecting work through issue #08.
**Scope:** System design covering registry, presence, commands, telemetry, and Edge UI proxy. Mobile readiness is a first-class design constraint. [Modules and implementation status](#modules-and-implementation-status) records what is built so far.

## Goals

- Single dashboard to see online/offline status and key telemetry for all uKnomi edge devices.
- Run remote commands (service restart, run script, reboot) safely and audited, without SSHing into devices.
- Proxy access to each device's local Edge UI (cameras, configuration screens) from the dashboard.
- Multi-OS today (macOS + Linux), with Linux as a deprecating-but-supported tier.
- API-first so a future mobile app for site operators can be added without re-platforming.

## Non-goals

- Not an MDM. Mosyle handles Mac provisioning; CP handles runtime management.
- Not a streaming/RTSP service. Camera live feeds were unreliable; the local Edge UI serves snapshot feeds, and CP proxies the local UI.
- Not a Zabbix replacement (Zabbix is being de-prioritized; CP telemetry covers CP's needs).
- Not multi-tenant SaaS. Single org (uKnomi) with internal staff and (future) scoped operator accounts.

## Constraints

- Devices sit behind NAT at client sites.
- All devices are on a single Tailscale tailnet.
- AWS infrastructure in US regions; clients are US-based; no cross-border data concerns.
- 24/7 uptime required.
- Small operator team (uKnomi staff today; field operators in future).

## High-level architecture

```mermaid
flowchart TB
    clients["Web dashboard · Mobile app (future)"]

    subgraph edge["Edge device — Mac Mini / Pi / Radxa"]
        agent["uknomi-agent (Go binary)<br/>heartbeat · telemetry · command executor"]
        edgeui["Edge UI (Flask, Mac only)<br/>bound to 127.0.0.1:5050"]
        tsd["Tailscale daemon"]
        agent --> edgeui
    end

    subgraph aws["AWS — single US region"]
        alb["ALB"]
        iot["AWS IoT Core<br/>MQTT/mTLS · device shadow"]
        rule["IoT Rules: heartbeat + lifecycle"]
        sqs["SQS presence queues (+ DLQs)"]

        subgraph fargate["ECS Fargate"]
            api["cp-api<br/>enrollment · device reads · auth"]
            ingest["cp-ingest<br/>SQSConsumer + PresenceIngester"]
            dash["dashboard (Next.js)"]
            tsr["Tailscale subnet router"]
        end

        pg[("RDS Postgres — multi-AZ<br/>devices · operators · audit")]
        s3[("S3 — binaries · cmd output · audit mirror")]
        kms["KMS — command-signing key"]
        sm["Secrets Manager"]
    end

    clients -->|HTTPS + JWT| alb
    alb --> api
    alb --> dash
    agent -->|MQTT over mTLS| iot
    iot --> rule --> sqs --> ingest
    api -->|provision thing + cert| iot
    api --> pg
    ingest --> pg
    api --> s3
    api -.->|sign commands| kms
    api -.->|secrets| sm
    api -->|Edge UI proxy| tsr
    tsr -.->|tailnet| tsd
```

The data path splits in two: the **command/telemetry channel** runs over MQTT to AWS IoT Core; the **Edge UI proxy path** runs over Tailscale (ADR-003). Heartbeats and IoT lifecycle events reach Postgres asynchronously — IoT Rules route them through SQS, where `cp-ingest` consumes them (ADR-018).

## Components

### Edge device agent (`uknomi-agent`)

Single Go binary, cross-compiled to `darwin/arm64`, `darwin/amd64`, `linux/arm64`. Build-tag separated service backend (launchd vs systemd). Installed as a LaunchDaemon on macOS, a systemd unit on Linux.

Responsibilities:
- Maintain persistent MQTT connection to AWS IoT Core (X.509 mTLS).
- Publish heartbeat (every 30s) and telemetry (CPU, mem, disk, service states).
- Subscribe to per-device command topic; verify Ed25519 signatures before executing.
- Execute commands by shelling out to OS primitives (`launchctl`, `systemctl`, `tailscale`) or HTTP-calling localhost Edge UI on Macs.
- Self-update from a signed manifest in S3.

Explicitly does **not** reimplement Edge UI logic — wraps it.

### Command channel: AWS IoT Core

MQTT-over-WSS, per-device X.509 mTLS, device shadow used for desired/reported service state.

Topics:
- `devices/{id}/cmd` — CP → device, signed command payloads
- `devices/{id}/cmd-result` — device → CP, command outcomes
- `devices/{id}/telemetry` — device → CP, periodic metrics (heartbeat rides this topic)
- `$aws/things/{id}/shadow/...` — managed desired/reported state

An IoT Rule (`presence-heartbeat`) selects `devices/+/telemetry`, adds the `{id}` topic segment as `device_id`, and routes each message to an SQS queue for `cp-ingest`. See [decisions.md ADR-001](decisions.md#adr-001-aws-iot-core-for-command-channel) and ADR-018.

### Control Plane API service (`cp-api`)

Standalone HTTP API, written in Go (see ADR-009), deployed on ECS Fargate. REST today; a WebSocket channel for live dashboard events is later-phase. Web dashboard and (future) mobile app are equal clients.

Responsibilities:
- Authenticate clients directly (see ADR-010): username + Argon2id password + mandatory TOTP, issuing JWT bearer tokens. No external IdP.
- Expose endpoints for devices, sites, clients, commands, enrollment, audit, operators.
- Sign command payloads (Ed25519 key in KMS) and publish to IoT Core.
- Reverse-proxy to each device's localhost Edge UI via the tailnet (camera snapshots, embedded UI access).
- Validate idempotency keys on all state-mutating endpoints (ADR-012).

### Ingest workers (`cp-ingest`)

ECS Fargate worker, separate from `cp-api`, that consumes the MQTT-side data path (ADR-018 — Fargate, not Lambda, so a long-lived consumer can hold the SQS poll loop and drain gracefully).

- Generic `SQSConsumer[T]` long-polls a queue, validates each message's `correlation_id`, dispatches to a handler, and routes poison messages (bad JSON, missing `correlation_id`, unknown device) to a dead-letter queue.
- `PresenceIngester` is the heartbeat handler: it stamps `devices.last_seen`, marks the device online, and records the heartbeat in the in-memory presence model.
- `LifecycleIngester` consumes a second queue fed by IoT Core `connected`/`disconnected` events — the fast-path online↔offline edge, so a device that drops shows offline within seconds rather than waiting out the freshness threshold.
- `PresenceSweeper` is a goroutine, not an SQS consumer: every 30s it flips devices whose last heartbeat is older than the 90s threshold to offline — the backstop for a device that dies without a clean disconnect.
- Later phases reuse `SQSConsumer[T]` for command results and other ingest concerns.

### Dashboard (Next.js)

Operator-facing web UI, deployed on ECS Fargate. Thin client: posts username + password + TOTP to the Go API's `/auth/login` endpoint, stores the returned JWT, and uses it as a bearer token for every subsequent request. No server-side sessions exclusive to web. Mobile (future) uses the same auth endpoint.

Calls the API service for all data and actions; no direct AWS SDK use from the browser.

### Storage

- **RDS Postgres (multi-AZ)** — source of truth for clients, sites, devices, services, commands, audit log, operators, notification targets. Device presence is the stored `is_online` column on the `devices` row (alongside `last_seen` and `presence_changed_at`), maintained by `cp-ingest`. The `devices` row also stores `mtls_cert_expires_at` — the per-device mTLS cert's notAfter, captured at enrollment and surfaced on `GET /devices/{id}` as the early-warning signal for cert rotation (ADR-013). Schema is managed by goose migrations embedded in the binaries and applied on startup (ADR-019).
- **Timestream** — time-series telemetry metrics (CPU/mem/disk, per-service uptime); planned per ADR-016. Heartbeat *presence* does not use Timestream — it is the `last_seen` column in Postgres.
- **S3** — agent binaries (signed manifests for self-update), command stdout/stderr, camera snapshots if cached, daily audit-log mirror.

### Tailscale subnet router (Fargate)

A small Fargate task running the Tailscale client, joined to the uKnomi tailnet, advertising itself as a subnet router. The API service routes Edge UI proxy traffic through this task. Mobile and web clients never need tailnet membership.

### Auth

- **Devices** — X.509 mTLS, certs issued by IoT Core's CA, per-device thing identity. Phase 1 cert TTL is 1 year (see ADR-013).
- **Operators (web + future mobile)** — JWT bearer tokens issued by the Go API after username + Argon2id password + mandatory TOTP. No external IdP; staff and (future) field operators authenticate the same way (see ADR-010, which supersedes ADR-006).
  - TOTP (RFC 6238) is enrolled once via `POST /auth/totp/enroll`, which returns an `otpauth://` provisioning URI plus ten single-use recovery codes. The shared secret is stored AES-256-GCM-encrypted (key from `TOTP_ENCRYPTION_KEY`, a KMS-protected secret loaded at startup — the same handling as the JWT signing key); recovery codes are stored Argon2id-hashed.
  - Until an operator completes enrollment, the `RequireTotpEnrolled` gate answers every authenticated route except `/auth/totp/enroll` with `403` + a `Reason: totp-enrollment-required` header, and `POST /auth/login` returns `requires_totp_enrollment` so the client routes into enrollment.
- **Service-to-service inside AWS** — IAM roles; no shared secrets between Fargate tasks.

## Modules and implementation status

A map from the design above to the Go packages and binaries in the repo, and which issue landed each. "Built" means merged with tests; "planned" means designed but not yet implemented.

Binaries (`cmd/`):

| Binary | Role | Status |
|---|---|---|
| `cp-api` | HTTP API — enrollment, device reads, auth | Built (#03, #04, #05) |
| `cp-ingest` | Fargate worker — heartbeat → `last_seen` | Built (#07) |
| `agent` | `uknomi-agent` device binary | Built (Phase 0) |

Control Plane packages (`internal/cp/`):

| Package | Responsibility | Status |
|---|---|---|
| `registry` | Enrollment-first device lifecycle — `Enroll`, `GetByID`, `UpdateLastSeen` | Built (#03, #07, #08, #09) |
| `iotprovisioner` | Wraps the AWS IoT SDK — thing + certificate minting | Built (#03) |
| `authn` | Argon2id passwords, HS256 JWTs, refresh-token rotation, first-run admin, account lockout, mandatory TOTP + recovery codes | Built (#04, #05) |
| `presence` | Online threshold; in-memory per-device presence state and transitions (heartbeat, sweep, connect/disconnect) | Built (#07, #08) |
| `sqsconsumer` | Generic `SQSConsumer[T]` — schema validation, DLQ routing, graceful drain | Built (#07) |
| `ingest` | Heartbeat + lifecycle SQS handlers and the presence sweeper | Built (#07, #08) |
| `cplog` | Structured JSON logs + end-to-end correlation IDs (ADR-011) | Built (#19) |
| `storage` | Goose migrations (ADR-019), idempotency store | Built (#03) |
| `api` | HTTP router; idempotency, bearer-auth, and forced-TOTP-enrollment middleware | Built (#03, #04, #05) |

Not yet built: site-scoped authorization (#06), the Next.js dashboard (#16–#18 — including the per-device view that renders the cert-expiry fields `GET /devices/{id}` now returns), the `audit_log` table and surface (#20 — audit events are structured log lines until then), CloudWatch alarms (#21), and command execution (Phase 3).

## Cloud infrastructure

```mermaid
flowchart TB
    internet(("Internet"))
    iot["AWS IoT Core (managed, regional)"]

    subgraph vpc["VPC — single US region"]
        subgraph public["Public subnets"]
            alb["Application Load Balancer"]
        end
        subgraph private["Private subnets — multi-AZ"]
            api["Fargate: cp-api"]
            ingest["Fargate: cp-ingest"]
            dash["Fargate: dashboard"]
            tsr["Fargate: tailscale-subnet-router"]
            rds[("RDS Postgres — multi-AZ")]
        end
    end

    sqs["SQS: presence queues + DLQs"]
    s3[("S3")]
    kms["KMS"]
    sm["Secrets Manager"]
    cw["CloudWatch — Logs + Alarms"]

    internet --> alb
    alb --> api
    alb --> dash
    iot -->|IoT Rules| sqs --> ingest
    api --> iot
    api --> rds
    ingest --> rds
    api --> s3
    api --> kms
    api --> sm
    ingest --> sm
    api --> cw
    ingest --> cw
    dash --> cw
```

Infrastructure is Terraform, in `infra/terraform/` (ADR-015 multi-AZ Postgres, ADR-018 Fargate, ADR-021 all-CloudWatch observability). Current state:

- **Built** — `modules/sqs-ingest` (SQS queue + DLQ + redrive + IoT Rule) and `modules/cp-ingest-service` (Fargate task + service + log group), landed with #07; #08 reuses `sqs-ingest` for the presence-lifecycle queue.
- **Phase 0 spike** — the flat root in `infra/terraform/` provisions a single IoT thing + certificate for the agent spike.
- **Pending #01** — the Phase 1 root: VPC, subnets, ALB, the RDS instance, the Fargate cluster, S3 backend + DynamoDB lock for Terraform state. The modules above are consumed by that root.

## Key flows

### Enrollment

A device enrolls once, on first install. The install package carries a static **bootstrap key** (ADR-017 — a shared secret bundled at build time; superseded the per-device S3 token of ADR-014). `POST /enrollments` is idempotent on `hardware_uuid` (ADR-012), so a retried install over a flaky link does not double-register.

```mermaid
sequenceDiagram
    participant I as Install script
    participant A as cp-api
    participant IoT as AWS IoT Core
    participant DB as Postgres

    I->>A: POST /enrollments (Idempotency-Key: hardware_uuid)<br/>{bootstrap_key, hostname, hardware_uuid,<br/>hardware_kind, os_version, agent_version}
    A->>A: validate bootstrap_key (401 if wrong)
    A->>IoT: create thing + X.509 certificate
    IoT-->>A: thing ARN, certificate + private key
    A->>DB: insert device row
    A-->>I: 201 {device_id, mtls_cert_pem, mtls_private_key_pem,<br/>iot_endpoint, iot_thing_arn, mtls_cert_expires_at}
    I->>I: write certs, install + start the agent
    Note over A,DB: agent connects to IoT Core and publishes its first<br/>heartbeat; cp-ingest sets last_seen → device shows online
```

A replay of a prior `hardware_uuid` returns the original `201` response from the idempotency store. Linux devices run the same flow from a one-page install script (no full rollout repo — Pis are deprecating).

### Command execution

```
Operator clicks "Restart Edge UI" on the device page
  ↓
POST /devices/{id}/commands  { action: "service.restart", args: {name: "edge-ui"} }
   Idempotency-Key: <client-generated UUID>
  ↓
API validates auth, records pending command in Postgres,
signs payload with Ed25519 key (KMS), publishes to devices/{id}/cmd
  ↓
agent receives, verifies signature, executes via launchctl
  ↓
agent publishes to devices/{id}/cmd-result with stdout/stderr/exit_code
  ↓
ingest worker updates command record, mirrors stdout to S3
  ↓
API emits WebSocket event → dashboard updates live
```

Command execution is a Phase 3 concern; the diagram is the intended design. Audit log captures: who issued the command, when, full payload, signature hash, result, duration.

### Edge UI / camera access

```
Operator clicks "Open Edge UI" for device 7
  ↓
Browser opens https://cp.uknomi.example/devices/7/edge-ui
  ↓
API service (on tailnet via subnet router) reverse-proxies
to http://<device-7-tailscale-ip>:5050
  ↓
Operator interacts inline; all traffic is auth'd at CP boundary
```

This works identically for the future mobile app — clients never touch Tailscale themselves.

## Mobile readiness

The immediate deliverable is a web dashboard. A mobile app for field operators is anticipated for the install/rollout workflow at client sites. The architecture is shaped today so that mobile is a clean addition rather than a re-platform:

- **Backend is a standalone API service**, not a Next.js server-actions monolith. Web and mobile are equal API clients.
- **Auth tokens are JWT bearer**, issued by the Go API's `/auth/login` (password + TOTP, ADR-010). Mobile and web use the identical endpoint and JWT shape — no external IdP, no per-client auth path.
- **Idempotency keys** on all state-mutating endpoints — a flaky cellular link in a client's server closet will not double-create enrollments.
- **WebSocket channel** for live updates is consumable by web and mobile equally.
- **Edge UI / camera proxying lives on the API service** (which sits on the tailnet). Mobile clients never enroll in the tailnet.
- **Install workflow has dedicated endpoints** (`POST /enrollments`, `GET /enrollments/{id}/status`, `POST /enrollments/{id}/validate`) — a mobile UX for "scan device serial → assign to site → watch install progress" maps to these without API changes.
- **Push-notification schema present from day one**: a `notification_targets` table is included even though only WebSocket is used initially. Adding APNs/FCM later means a worker, not a refactor.

What is **not** decided now and does not need to be: framework (React Native vs Flutter vs PWA), native UX, app store distribution, push provider (SNS vs direct APNs/FCM). These don't affect today's design.

See [decisions.md ADR-005](decisions.md#adr-005-api-first-design-for-mobile-readiness).

## Security

- Per-device X.509 certs issued by IoT Core's CA; 1-year TTL in Phase 1 (ADR-013), with rotation tooling a later-phase concern.
- All commands signed with an Ed25519 key in KMS; agents reject unsigned or invalid commands.
- API authn: short-lived JWT bearer tokens (~1h), refreshed via rotating, hashed-at-rest refresh tokens (ADR-010) — no external IdP.
- Per-site authorization on operator JWTs (site allowlist claim, enforced server-side on every endpoint).
- Secrets in AWS Secrets Manager (Mosyle/Tailscale tokens, DB DSN, signing-key passphrase).
- Append-only audit log in Postgres + daily S3 mirror, covering: command issuance, login, config change, enrollment.
- Edge UI bound to `127.0.0.1` — only the agent (and via the tailnet, the CP proxy) can reach it. Reduces today's attack surface, where the Edge UI is reachable across the tailnet.

## Open questions

Resolved during 2026-05-18 design review; each links to its ADR:

- ~~**Postgres HA**~~ → resolved: multi-AZ from day one (ADR-015).
- ~~**API language**~~ → resolved: Go (ADR-009).
- ~~**Bootstrap token distribution**~~ → resolved: static key bundled in the install package (ADR-017, superseding the S3 approach of ADR-014).
- ~~**Telemetry retention**~~ → resolved: 30 days hot in Timestream, 1 year cold in S3 (ADR-016).

Still open:

- **Push provider** — defer until mobile work begins.
