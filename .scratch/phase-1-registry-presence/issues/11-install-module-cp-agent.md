# Issue 11 — mac-mini-rollout install module for CP agent

Status: ready-for-human
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

- [ ] On a clean Mac with the install package, running the module enrolls the device against a live CP and registers the LaunchDaemon. *(On-device — verified by Wave 0 / #12.)*
- [ ] The agent connects to IoT Core within 30s of the daemon starting and publishes its first heartbeat. *(On-device — module polls the agent's err log for 45s and exits non-zero if no connect; Wave 0 confirms.)*
- [ ] Re-running the module on the same Mac (same hardware UUID) is idempotent — no duplicate `devices` row, the existing cert is reused (the CP endpoint returns the original response from its idempotency table). *(Structural: `Idempotency-Key: <hardware_uuid>` + the module's `launchctl unload`-before-load. End-to-end check at Wave 0.)*
- [x] The bootstrap key is not visible in any log produced by the install module. *(Key only ever in a 0600 file; read by python via env-passed path; request body sent through `--data @file`; never echoed or assigned to a shell variable on the production path.)*
- [x] The module exits non-zero with a clear message if any step fails (network, auth, daemon registration). *(`run()` is a `set -e` subshell; every failure path is `error "..."; exit 1`; the case-statement maps 401/429/network/other to distinct messages.)*
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 03 (enrollment endpoint).
- Issue 10 (bootstrap key in Secrets Manager + CI integration, production hardening).

## Comments

### 2026-05-22 — module written and committed in mac-mini-rollout

Implementation lives in the sister repo at `../mac-mini-rollout`, which
was previously not under version control. With the user's explicit
sign-off the repo was `git init`'d (baseline commit `2d7eeaf`) and the
#11 work landed as commit `6a2aeed`:

- `modules/11-cp-agent.sh` — reads the baked-in bootstrap key and the
  Mac's hardware identifiers (`ioreg` UUID, `scutil` hostname, `sw_vers`
  OS version), POSTs to `<CP>/enrollments` with
  `Idempotency-Key: <hardware_uuid>`, installs the returned mTLS cert
  and private key to `/var/uknomi/{cert,key}.pem` (0600, root), writes
  the agent's JSON config, drops `/Library/LaunchDaemons/com.uknomi.agent.plist`,
  `launchctl load`s it, and polls the agent's err log for an
  `agent started` line within 45s.
- `uninstall-cp-agent.sh` — stops the daemon, removes the binary and
  `/var/uknomi`, and prints the manual `aws iot delete-certificate`
  steps (Phase 1 has no CP revocation endpoint).
- `setup.sh` — registers `11-cp-agent` in `get_phase_map` as Phase 1.
- `.env.example` — documents `CP_BASE_URL` / `CP_BROKER_URL`.

**Premise corrections / findings.** The issue and the agent surface
turned out not to match in a few places:

1. *`setup.sh` phase map* — without an entry, a phased run (`--phase 1`)
   would silently skip the new module. Added.
2. *Enrolment response `iot_endpoint`* — the issue says "drop a
   LaunchDaemon plist with the IoT endpoint from the response", but
   `internal/cp/api/handlers/enrollment` never populates that field, so
   the response carries it as the empty string. The module reads the
   IoT broker URL from a `CP_BROKER_URL` config var instead — the
   endpoint is fleet-wide anyway. **Worth a CP-side follow-up** to
   populate `iot_endpoint` (or remove it from the response struct).
3. *Hostname-convention regex* — the CP's `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$`
   will not match the rollout repo's staging or production hostnames
   (`macmini-staging-...`, `STORE-CHAIN-LOCATION-macmini`), so every
   Mac enrolled by this module will fire the (non-blocking)
   `audit.enrollment.anomaly` alert. Cross-repo naming inconsistency —
   needs reconciliation in one repo or the other.
4. *Agent version* — `cmd/agent` has no `--version` flag (version comes
   from its config file), so the module reads it from a baked
   `bin/uknomi-agent.version` text file, falling back to `unknown`.
5. *AWS IoT server CA* — the enrolment response gives the device cert
   but not the CA. The module bundles `certs/AmazonRootCA1.pem` if
   present, else downloads it from amazontrust.com.

**Verification status.** `bash -n` clean on the module, the uninstall
script, and the modified `setup.sh`. The on-device ACs (AC1–3) need a
real Mac + a live CP — they are verified by Wave 0 (#12). AC4 and AC5
are satisfiable by inspection of the script and are checked here.

**AC2 of #10** (the `mac-mini-rollout` CI workflow that bakes the
bootstrap key into `secrets/cp-bootstrap-key`) is still deferred and is
filed against the rollout repo's tracker — separate from #11's scope.

**Documentation criterion.** Discharged — CP `docs/architecture.md`
§ Enrollment already describes the install-script → `POST /enrollments`
flow abstractly; the concrete module is a sister-repo artifact and does
not change the CP architecture. `CONTEXT.md` unchanged: no new domain
term. No ADR — the script implements ADR-017's flow, not a new decision.

**Status.** Set to `ready-for-human` rather than `done`: the deliverable
is written and committed, but acceptance is end-to-end and lives on the
bench Mac during Wave 0. Closing #11 is Wave 0's call.
