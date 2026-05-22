# Issue 11 — mac-mini-rollout install module for CP agent

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 2, § Deliverables.
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — "New `mac-mini-rollout/modules/11-cp-agent.sh` that installs the agent and enrolls."

## What to build

The Mac install module that runs at provisioning time: presents the baked-in bootstrap key, calls `POST /enrollments`, installs the returned mTLS cert + private key, registers the agent as a LaunchDaemon, and starts it. Lives in the sister repo `mac-mini-rollout/`.

Scope (in the `mac-mini-rollout` repo, not this one):

- `mac-mini-rollout/modules/11-cp-agent.sh` (new):
  - Reads the bootstrap key from the install package (baked at CI build time per #10).
  - Reads hardware identifiers from the Mac (hardware UUID via `ioreg`, hostname via `scutil`, OS version via `sw_vers`, agent binary version from the embedded binary).
  - POSTs to `<CP base URL>/enrollments` with `Idempotency-Key: <hardware_uuid>` and the bootstrap key.
  - Writes the returned cert + key to a protected path (`/var/uknomi/cert.pem`, `/var/uknomi/key.pem`, mode 0600, owner `root`).
  - Drops a LaunchDaemon plist that runs `uknomi-agent` with the cert/key paths and the IoT endpoint from the response.
  - `launchctl load`s the daemon and waits for the first heartbeat.
- The `uknomi-agent` binary itself is the Phase 0 artifact, no changes needed to the agent for this slice.
- A small `uninstall.sh` companion script that revokes the cert (via a CP endpoint to be defined in Phase 3 — for Phase 1, manual `aws iot delete-certificate` per the decommission runbook).

Out of scope: re-enrollment if the cert is lost (Phase 4); cert rotation (Phase 4); self-update (Phase 3).

## Acceptance criteria

- [ ] On a clean Mac with the install package, running the module enrolls the device against a live CP and registers the LaunchDaemon.
- [ ] The agent connects to IoT Core within 30s of the daemon starting and publishes its first heartbeat.
- [ ] Re-running the module on the same Mac (same hardware UUID) is idempotent — no duplicate `devices` row, the existing cert is reused (the CP endpoint returns the original response from its idempotency table).
- [ ] The bootstrap key is not visible in any log produced by the install module.
- [ ] The module exits non-zero with a clear message if any step fails (network, auth, daemon registration).
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 03 (enrollment endpoint).
- Issue 10 (bootstrap key in Secrets Manager + CI integration, production hardening).
