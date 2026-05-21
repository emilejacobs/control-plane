# Architecture

**Status:** Proposed for review, 2026-05-05.
**Scope:** Initial design covering registry, presence, commands, telemetry, and Edge UI proxy. Mobile readiness is a first-class design constraint.

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

```
┌─ Edge device (Mac Mini / Pi / Radxa) ─────────────┐
│                                                    │
│  uknomi-agent (Go binary)                         │
│   ├─ launchd (macOS) / systemd (Linux)            │
│   ├─ MQTT-over-WSS to AWS IoT Core (mTLS)         │
│   ├─ heartbeat, telemetry, command executor       │
│   └─ wraps Edge UI on Mac (localhost HTTP)        │
│                                                    │
│  Edge UI (Flask, Mac only, bound to 127.0.0.1)    │
│  Tailscale daemon ◄───── data path ──────┐         │
└──────────────────────────────────────────┼─────────┘
                       │ (outbound TLS)    │
                       ▼                   │
┌─ AWS (us-east-1 or us-west-2) ─────────────────────┐
│                                                     │
│  AWS IoT Core ───── command + telemetry topics     │
│   │  device shadow (desired/reported state)        │
│   ▼                                                 │
│  ECS Fargate workers (ADR-018)                     │
│   ├─ command dispatcher (Phase 3)                  │
│   ├─ telemetry/presence ingest (Phase 1+)          │
│   └─ enrollment handler                            │
│                                                     │
│  ECS Fargate tasks                                 │
│   ├─ API service (Go)  ◄──────────────┐           │
│   ├─ Dashboard (Next.js)                │           │
│   └─ Tailscale subnet router  ──────────┘ (proxy   │
│       to Edge UI / device localhost via tailnet)   │
│                                                     │
│  RDS Postgres (multi-AZ) — registry, commands,     │
│                            audit, operators        │
│  Timestream  — heartbeats, metrics                 │
│  S3          — agent binaries, command output,     │
│                snapshots, audit mirror             │
│  KMS         — command-signing key (Ed25519)       │
│  Secrets Mgr — Mosyle / Tailscale tokens           │
│                                                     │
│  ALB ──► API + Dashboard                           │
│  Route 53 + ACM                                    │
└────────────────────────────────────────────────────┘
                       ▲
                       │ HTTPS + JWT
                       │
              ┌────────┴────────┐
              │ Web Dashboard   │  (uKnomi staff today)
              │ Mobile App      │  (field operators, future)
              └─────────────────┘
```

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
- `devices/{id}/telemetry` — device → CP, periodic metrics
- `$aws/things/{id}/shadow/...` — managed desired/reported state

See [decisions.md ADR-001](decisions.md#adr-001-aws-iot-core-for-command-channel) for rationale.

### Control Plane API service

Standalone HTTP API (REST + WebSocket), written in Go (see ADR-009), deployed on ECS Fargate. Web dashboard and (future) mobile app are equal clients. The OpenAPI spec is the contract of record; the dashboard generates a typed TS client from it.

Responsibilities:
- Authenticate clients directly (see ADR-010): username + Argon2id password + mandatory TOTP, issuing JWT bearer tokens. No external IdP.
- Expose endpoints for devices, sites, clients, commands, enrollment, audit, operators.
- Sign command payloads (Ed25519 key in KMS) and publish to IoT Core.
- Reverse-proxy to each device's localhost Edge UI via the tailnet (camera snapshots, embedded UI access).
- Emit WebSocket events for live dashboard updates.
- Validate idempotency keys on all state-mutating endpoints.

### Dashboard (Next.js)

Operator-facing web UI, deployed on ECS Fargate. Thin client: posts username + password + TOTP to the Go API's `/auth/login` endpoint, stores the returned JWT, and uses it as a bearer token for every subsequent request. No NextAuth, no server-side sessions exclusive to web. Mobile (future) uses the same auth endpoint.

Calls the API service for all data and actions; no direct AWS SDK use from the browser.

### Storage

- **RDS Postgres (multi-AZ)** — source of truth for clients, sites, devices, services, commands, audit log, operators, notification targets.
- **Timestream** — time-series telemetry (heartbeat, CPU/mem/disk, per-service uptime). Cheap, serverless, well-suited.
- **S3** — agent binaries (signed manifests for self-update), command stdout/stderr, camera snapshots if cached, daily audit-log mirror.

### Tailscale subnet router (Fargate)

A small Fargate task running the Tailscale client, joined to the uKnomi tailnet, advertising itself as a subnet router. The API service routes Edge UI proxy traffic through this task. Mobile and web clients never need tailnet membership.

### Auth

- **Devices** — X.509 mTLS, certs issued by IoT Core's CA, per-device thing identity. Phase 1 cert TTL is 1 year (see ADR-013).
- **Operators (web + future mobile)** — JWT bearer tokens issued by the Go API after username + Argon2id password + mandatory TOTP. No external IdP; staff and (future) field operators authenticate the same way (see ADR-010, which supersedes ADR-006).
- **Service-to-service inside AWS** — IAM roles; no shared secrets between Fargate tasks.

## Key flows

### Enrollment

```
Mac Mini boots → setup.sh → modules/11-cp-agent.sh
  ↓
fetches one-time bootstrap token from S3 (signed, expiring)
  ↓
POST /enrollments  { token, hardware_info, hostname, tailscale_ip }
   Idempotency-Key: <hardware_uuid>
  ↓
API validates token, creates device record, registers thing in IoT Core,
returns mTLS cert + private key (one-time-fetch URL)
  ↓
agent installs cert, registers as LaunchDaemon, starts
  ↓
agent connects to IoT Core, publishes first heartbeat
  ↓
device transitions to "online" in dashboard
```

For Linux devices the same flow runs from a one-page install script (no full rollout repo — Pis are deprecating).

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

Audit log captures: who issued the command, when, full payload, signature hash, result, duration.

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
- **Auth tokens are JWT bearer**, issued by the same flow NextAuth uses. Mobile uses a native OIDC library for Entra ID and a username/password+TOTP flow for local accounts; both yield the same JWT shape.
- **Idempotency keys** on all state-mutating endpoints — a flaky cellular link in a client's server closet will not double-create enrollments.
- **WebSocket channel** for live updates is consumable by web and mobile equally.
- **Edge UI / camera proxying lives on the API service** (which sits on the tailnet). Mobile clients never enroll in the tailnet.
- **Install workflow has dedicated endpoints** (`POST /enrollments`, `GET /enrollments/{id}/status`, `POST /enrollments/{id}/validate`) — a mobile UX for "scan device serial → assign to site → watch install progress" maps to these without API changes.
- **Push-notification schema present from day one**: a `notification_targets` table is included even though only WebSocket is used initially. Adding APNs/FCM later means a worker, not a refactor.

What is **not** decided now and does not need to be: framework (React Native vs Flutter vs PWA), native UX, app store distribution, push provider (SNS vs direct APNs/FCM). These don't affect today's design.

See [decisions.md ADR-005](decisions.md#adr-005-api-first-design-for-mobile-readiness).

## Security

- Per-device X.509 certs issued by IoT Core's CA, rotated every 90 days.
- All commands signed with an Ed25519 key in KMS; agents reject unsigned or invalid commands.
- API authn: short-lived JWT bearer tokens (~1h), refresh via OIDC for staff and local refresh tokens for operators.
- Per-site authorization on operator JWTs (site allowlist claim, enforced server-side on every endpoint).
- Secrets in AWS Secrets Manager (Mosyle/Tailscale tokens, signing-key passphrase).
- Append-only audit log in Postgres + daily S3 mirror, covering: command issuance, login, config change, enrollment.
- Edge UI bound to `127.0.0.1` — only the agent (and via the tailnet, the CP proxy) can reach it. Reduces today's attack surface, where the Edge UI is reachable across the tailnet.

## Open questions

Resolved during 2026-05-18 design review; each links to its ADR:

- ~~**Postgres HA**~~ → resolved: multi-AZ from day one (ADR-015).
- ~~**API language**~~ → resolved: Go (ADR-009).
- ~~**Bootstrap token distribution**~~ → resolved: S3 (ADR-014).
- ~~**Telemetry retention**~~ → resolved: 30 days hot in Timestream, 1 year cold in S3 (ADR-016).

Still open:

- **Push provider** — defer until mobile work begins.
