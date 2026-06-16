# ADR-036: CP-driven device lifecycle — Provision / Assign / Commission

**Status:** Accepted (2026-06-15)

**Refines:** [ADR-004](./0004-install-script-enrollment.md) (install-script-driven enrollment). Retires the `mac-mini-rollout` two-phase (HQ-staging / on-site-activation) install model.

**Context.**

The `mac-mini-rollout` install system predates the Control Plane. It used a device-local two-phase Bash framework: **Phase 1** (HQ staging — generic config) and **Phase 2** (on-site activation — store identity, hostname, inventory, S3 registration, store-specific service config). The CP is now the authoritative device registry and config source (`devices`, `sites`, `device_cameras`, service allow-list, snapshot cadence), so much of Phase 2 — naming, `08-inventory`'s local CSV/`device-config.json`, `10-s3-register`'s pipe-delimited S3 registry — duplicates or shadows what the CP already owns. The two-phase model also forces an on-site Bash run and collides terminologically with the CP project's own "Phase 1/2/3" rollout axis. A design pass (grill-with-docs, 2026-06-15) resolved to collapse install into a CP-driven lifecycle. The reachability fact that makes this safe: `POST /enrollments` is on a public ALB and MQTT is public AWS IoT Core, so a device enrolls and becomes commandable **before** it has any site identity or tailnet membership.

**Decision.**

1. **Three steps replace the two-phase model.** **Provision** (one-shot, device-side, generic — includes Enrollment), **Assign** (operator binds the enrolled device to a Site in the dashboard, setting `devices.site_id`), **Commission** (CP pushes all site-specific config). Between Provision and Assign a device is **enrolled-but-unassigned**: online, idle, NULL `site_id`, visible in the rollout list. Terms recorded in [CONTEXT.md](../CONTEXT.md); "Phase 1/Phase 2" is retired for install and reserved for CP rollout phases.

2. **Uniform install, CP-activated capabilities.** Every Mac gets the identical software set at Provision — no capability branching mid-install. The CP *activates* capabilities at Commission. The per-device ALPR license is consumed only when the CP activates ALPR (container start); the whisper model is bundled on every device regardless (disk only).

3. **Commission delivery over the existing MQTT command channel,** consistent with `cameras.update` / `config.update`: cameras, ALPR `config.ini` + license, Tailscale auth key, service allow-list, snapshot cadence. Secret-bearing messages are **non-retained**; the agent persists secrets **0600 root**; authenticity hardens when Phase 3 envelope signing ([ADR-028](./0028-unsigned-config-update-phase-2.md) → [ADR-035](./0035-agent-fleet-update-mechanism.md)) wraps the cmd.

4. **Per-device single-use Tailscale keys.** At Commission the CP mints an ephemeral, single-use, tagged Tailscale auth key per device via the Tailscale API and pushes it — replacing the shared fleet key. The CP holds a Tailscale API credential (Secrets Manager), used only by the Commission path.

5. **ALPR license is staff-supplied in the CP.** Plate Recognizer has no key-minting API, so the account-wide PR token is stored once in the CP and the per-device license is entered by staff on the device record; both are pushed at Commission.

6. **Existing fleet converges — it is not re-provisioned.** Already-enrolled devices: finish `migrate-fleet.sh` on the pre-supervisor stragglers → receive the new agent via self-update + a CP config backfill (`snapshot_state_path`, etc.) → a one-shot Docker→Colima migration driven by the `migrate-fleet.sh` SSH harness (operator-watched, per-device — see [ADR-038](./0038-colima-replaces-docker.md)).

**Consequences.**

- (+) No on-site Bash run; on-site work shrinks to racking + powering the Mac. Identity, config, and naming are all CP-driven post-enrollment.
- (+) Kills the Phase-2 duplication (`08-inventory`, `10-s3-register`) — the CP is the single source of truth.
- (+) Per-device single-use Tailscale keys remove the shared-key blast radius and rotation coupling.
- (+) Reuses existing CP→agent primitives (`cameras.update`, `config.update`, `snapshot.config`, `network.scan`) — Commission is mostly wiring, not new protocol.
- (−) The CP gains a Tailscale API credential + key-minting path and storage for the PR token/license — new surfaces.
- (−) Secrets (TS key, ALPR license) ride the unsigned MQTT command channel until Phase 3 signing; mitigated by per-device mTLS topic-scoping + non-retained delivery.
- (−) Auto-login becomes load-bearing (Colima/ALPR depend on the `uknomi` GUI session) — see [ADR-038](./0038-colima-replaces-docker.md).
- (−) Camera discovery + angle verification become a Commission-time operator workflow (`network.scan` → `cameras.update` → Edge UI "Verify angle"), not an on-site step.

**Verification.** TBD at implementation. Tests cover: Commission push round-trips (cameras / license / TS-key / allow-list), the enrolled-unassigned state in the registry, per-device Tailscale key minting (single-use + tagged), and existing-fleet config backfill. Infra surfaces (Tailscale API credential, PR token/license storage): `N/A — environmental/infra decision`.
