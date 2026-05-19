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
- AWS infra in Terraform/CDK: VPC, ALB, RDS Postgres (multi-AZ), Fargate cluster, IoT Core CA, S3 buckets, KMS keys, Tailscale subnet router task.
- API service skeleton with auth (NextAuth Entra ID + Credentials), enrollment endpoints, device list/detail endpoints, WebSocket for live updates.
- Next.js dashboard: login, fleet view (online/offline, by client/site), per-device view (read-only).
- New `mac-mini-rollout/modules/11-cp-agent.sh` that installs the agent and enrolls.
- One-page Linux install script for Pi/Radxa enrollment.
- Roll out to **all 63 devices**, read-only.

**Success criteria:**
- Operators check device status in the dashboard instead of the spreadsheet for one full week.
- Spreadsheet is retired (or marked deprecated).

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
