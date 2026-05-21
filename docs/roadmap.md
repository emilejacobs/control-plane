# Roadmap

Phased delivery designed to validate the architecture cheaply before committing to the full build, and to deliver operator value at each phase boundary.

Estimates are rough and assume one engineer focused on this work; halve them with two.

---

## Phase 0 — Cross-platform agent spike

**Estimate:** 1–2 weeks.
**Objective:** Prove the command channel works through real client NAT for both Mac and Linux.

**Deliverables:**
- `uknomi-agent` Go binary, builds for `darwin/arm64`, `darwin/amd64`, `linux/arm64`.
- AWS IoT Core thing per device, manually provisioned X.509 certs.
- Three commands wired end-to-end: `heartbeat`, `service.status <name>`, `service.restart <name>`.
- A small CLI on a developer laptop that publishes commands and prints responses.
- Deployed to **one Mac Mini** at a real client site and **one Pi** for Linux validation.

**Success criteria:**
- Agent reconnects automatically after network interruption.
- Commands round-trip in under 2 seconds when the device is online.
- Identical binary works on Mac and Pi (no Linux-specific patches needed).

**Risks:**
- Client-site firewalls block MQTT-over-WSS. Unlikely (it's just outbound HTTPS on 443) but a real risk worth surfacing early.

---

## Phase 1 — Registry, presence, and enrollment

**Estimate:** 4–6 weeks.
**Objective:** Replace the device spreadsheet (`uknomi-macmini-devices.xlsx`) with a live online/offline view of the whole fleet.

**Deliverables:**
- AWS infra in Terraform/CDK: VPC, ALB, RDS Postgres (multi-AZ), Fargate cluster, IoT Core CA, S3 buckets, KMS keys, Tailscale subnet router task. **Plus:** SQS queues for `cp-presence-heartbeats` and `cp-presence-lifecycle`, both with DLQs. **Timestream is deferred to Phase 2** — Phase 1 presence is derived from `last_seen` in Postgres, updated by the ingest worker from heartbeat messages. IoT Core lifecycle events provide the fast-path online → offline transition.
- `cp-ingest` Fargate worker (Go, ADR-018): SQS consumer with `PresenceIngester`, `LifecycleIngester`, and `PresenceSweeper` goroutines. No Lambda anywhere in the ingest path; uniform Fargate paradigm across all phases.
- API service skeleton with auth (local credentials + TOTP per ADR-010), enrollment endpoints, device list/detail endpoints. **No WebSocket in Phase 1** — live updates land in Phase 2 when command-results and live service-state actually need server-push.
- Next.js dashboard: login, fleet view (online/offline, by client/site), per-device view (read-only — static fields + presence + cert-expiry). Live updates via 10s polling against the query cache. **Structural rule: all live data flows through TanStack Query (or equivalent); no direct `setInterval` in components.** Preserves a cheap WebSocket migration in Phase 2.
- New `mac-mini-rollout/modules/11-cp-agent.sh` that installs the agent and enrolls.
- One-page Linux install script for Pi/Radxa enrollment (built, but rollout deferred — see below).
- Roll out to the **Mac fleet (~25 devices), read-only**, in four explicit waves:

  | Wave | Devices | Purpose | Exit before next wave |
  |---|---|---|---|
  | **0 — Bench** | 1 Mac on developer desk (re-use Phase 0 device) | Validate codified Terraform + new install module end-to-end on hardware we control | Smoke commands succeed; rollback tested |
  | **1 — Pilot site** | 1 client site, ~3–5 devices | Validate at a single real site with one operator watching the dashboard | One week stable, no manual intervention |
  | **2 — Mac fleet** | ~25 Macs across remaining sites | Bulk Mac rollout | All Macs online in dashboard, `last_seen` fresh |
  | **3 — Linux tail** | 36 Pis + 2 Radxas | Deferred — see "Linux deferral" below | Tracked separately, not gating Phase 1 |

**Linux deferral:** Linux rollout (Wave 3) is **out of Phase 1's exit gate**. Pi/Radxa are deprecating (ADR-007; Mac consolidation per project direction). The install script is built in Phase 1 so the path exists, but actually enrolling the 38 Linux devices is a parallel track that runs alongside or after Phase 2. The spreadsheet retirement gate is adjusted: Mac rows are removed from the spreadsheet at the retirement date; Linux rows get a "enroll via dashboard once ready" banner until they're either re-platformed to Mac or natively enrolled.

**Success criteria — two gates:**

- **Ship gate (technical):** all ~25 Macs enrolled in the registry through Waves 0–2; online/offline accurate within 60s; dashboard groups them by client/site; login + TOTP works for the operator set. Verifiable in a single sitting.
- **Retirement gate (operational):** Mac rows are removed from the spreadsheet on a calendar date the team picks (target: two weeks after ship gate); Linux rows get a "enroll via dashboard once ready" banner and stay in the spreadsheet until Wave 3 (parallel track, not gating Phase 1). We do not measure operator habit — we remove the alternative for the Mac portion.

**Risks:**
- Cert rotation flow not yet built — devices in Phase 1 use 1-year certs (see ADR-013). Rotation lands in Phase 4 on top of the Phase 3 self-update primitive; the 1-year TTL makes the deadline visible if Phase 4 slips.

---

## Phase 2 — Read operations

**Estimate:** 3–4 weeks.
**Objective:** Replace "SSH to check what's happening" with the dashboard.

**Deliverables:**
- Service-status reporting: per-device list of services and their state (`launchd` on Mac, `systemd` on Linux).
- Log tail endpoint: agent streams the last N lines of named logs to the API on demand.
- Edge UI proxy: dashboard embeds the local Edge UI inline via the Tailscale subnet router. Auth gated at CP boundary.
- Camera snapshot fetch (via the same proxy).

**Success criteria:**
- Operators no longer SSH for read-only checks.

**Risks:**
- Edge UI iframe-proxying may break some Edge UI features that assume cross-origin behavior. Mitigation: rebuild the most-used Edge UI screens natively in the CP if proxying is too lossy (Phase 4).

---

## Phase 3 — Write operations

**Estimate:** 3–5 weeks.
**Objective:** Stop logging into devices entirely.

**Deliverables:**
- Signed-command pipeline: API signs payloads with an Ed25519 KMS key; agent verifies before executing.
- Commands: `service.restart`, `service.start`, `service.stop`, `run-script` (script content signed and capped in size), `reboot`.
- **Agent self-update via signed S3 manifest, with auto-rollback** (see ADR-013): agent polls manifest + reacts to signed `agent.update` commands; supervisor reverts to last-known-good binary if no heartbeat within 5 minutes after a swap.
- Full audit log surface in the dashboard (who, when, what, result).
- Manual decommission action.

**Success criteria:**
- A real production restart that previously required SSH is performed via the dashboard.
- Audit log captures every command issued in a one-week window with no gaps.

**Risks:**
- Operator error (restarting the wrong service). Mitigation: confirmation dialogs on dashboard, dry-run mode for `run-script`.

---

## Phase 4 — Consolidation

**Estimate:** ongoing, 4–8 weeks of work spread over a quarter.
**Objective:** Retire transitional scaffolding and harden.

**Deliverables (priority order):**
- Cert rotation: built on the Phase 3 self-update primitive — CP issues a new cert and a signed command instructing the agent to install + reload. Default cycle: every 90 days, automated. Phase 1 ships 1-year certs, so this must land before month 10 to avoid a fleet-wide cert-expiry deadline.
- Native Edge UI screens in CP for the highest-traffic features (camera snapshots, service grid), reducing reliance on the iframe proxy.
- Periodic Mosyle reconciliation job (decommission devices removed from Mosyle).
- Telemetry retention policy enforcement (per ADR-016: 30d hot, 1y cold).
- Decision point: remove Zabbix from new rollouts (`mac-mini-rollout` decision).
- Decision point: retire S3-based device inventory (`mac-mini-rollout/modules/10-s3-register.sh`).

---

## Mobile app (post-Phase 4)

Not in this roadmap. The architecture (see ADR-005) ensures it can be added without backend rework. Trigger: when field-operator rollouts become a regular pattern rather than ad-hoc.
