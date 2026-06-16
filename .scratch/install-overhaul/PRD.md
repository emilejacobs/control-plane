# Install overhaul — CP-driven Provision / Assign / Commission

Design source of truth is the ADRs; this PRD adds the user stories, module interfaces, testing, and slice plan.

## Design source of truth

- **[ADR-036](../../docs/adr/0036-cp-driven-device-lifecycle.md)** — CP-driven lifecycle: **Provision → Assign → Commission**, retiring the two-phase model. Uniform install + CP-activated capabilities; Commission over MQTT; per-device single-use Tailscale keys; staff-entered ALPR license; existing fleet converges.
- **[ADR-037](../../docs/adr/0037-install-folded-into-agent-signed-pkg.md)** — install folded into `uknomi-agent install`, shipped as a Mosyle-pushed signed pkg; bootstrap key the only local secret; idempotent-by-inspection.
- **[ADR-038](../../docs/adr/0038-colima-replaces-docker.md)** — Colima replaces Docker Desktop; per-user VM, root agent drives the workload via `launchctl asuser`.

Supporting context: [ADR-004](../../docs/adr/0004-install-script-enrollment.md) (install-script enrollment, preserved), [ADR-017](../../docs/adr/0017-static-bootstrap-key-in-install-package.md) (bootstrap key), [ADR-035](../../docs/adr/0035-agent-fleet-update-mechanism.md) (supervisor + self-update), [ADR-034](../../docs/adr/0034-agent-backend-abstraction-os-agnostic-surface.md) (OS-agnostic backend split), [ADR-028](../../docs/adr/0028-unsigned-config-update-phase-2.md) (unsigned cmds → Phase 3 signing). Vocabulary: [CONTEXT.md](../../docs/CONTEXT.md) (Provision / Assign / Commission / Enrollment / Bootstrap key).

## Problem Statement

The `mac-mini-rollout` install process predates the Control Plane and the operator is not willing to keep extending it. It is a device-local two-phase Bash framework (`setup.sh` + `modules/NN-*.sh`): **Phase 1** (HQ staging) and **Phase 2** (on-site activation, where an on-site tech runs Bash to name the device, scan cameras, register it, and turn on store services). Concretely it is:

- **Brittle at the entry point** — one of two Mosyle bootstrap scripts writes a `.env` of 11 embedded credentials, including a Tailscale key *shared across the whole fleet* and a presigned S3 URL that silently expires after 7 days; `REPLACE_` placeholders are filled in by hand with no validation.
- **Duplicative of the CP** — Phase 2 still names devices, writes a local inventory CSV/`device-config.json`, and registers into a pipe-delimited S3 text file, all of which the CP is now the authoritative owner of (`devices`, `sites`, `device_cameras`).
- **Carrying dead weight** — Zabbix and AnyDesk modules for software being phased out; a `raven` no-op shim; Pi/Radxa assumptions on a Mac-only fleet.
- **Drift-prone and racy** — module `11-cp-agent` and the three `migrate-*` scripts each hand-generate the same LaunchDaemon plist + agent-config (4 copies that drift); none guards the supervisor's `trying` flag, so a re-run mid-update can corrupt a staged candidate.
- **Manual where it can't afford to be** — a Docker Desktop license click and hand-editing `config.ini` with camera RTSP URLs block on-site activation; the term "Phase 1/Phase 2" collides with the CP project's own rollout-phase axis.

## Solution

Collapse install into a **CP-driven lifecycle** with no on-site Bash run, folded into the existing Go agent binary and delivered by MDM:

- **Provision** — Mosyle pushes a signed `.pkg` that bundles the `uknomi-agent` binary + a bootstrap key (the only local secret) and runs `uknomi-agent install`. The installer self-configures the host, installs the uniform software set (Homebrew formulae, Colima, ffmpeg, whisper, the ALPR image), lays down the supervisor + LaunchDaemon, and **enrolls** against the public `POST /enrollments` (mTLS cert). Identical on every Mac; no site identity. Idempotent by inspection.
- **(enrolled, unassigned)** — the device is online and idle, NULL `site_id`, awaiting an operator in the rollout list.
- **Assign** — an operator binds the device to a **Site** in the dashboard (the #64 site-picker).
- **Commission** — the CP pushes everything site-specific over the existing MQTT command channel: `cameras.update`, the ALPR `config.ini` + per-device license (starts the Colima container), a freshly-minted **single-use Tailscale key** (joins the tailnet), the service allow-list, and snapshot cadence.

The on-site tech only racks + powers the Mac. Docker Desktop is replaced by **Colima** (CLI-only, no license click), with the container managed across the root↔user boundary via `launchctl asuser`. The existing fleet **converges** rather than re-installing: finish `migrate-fleet.sh` on the pre-supervisor stragglers → new agent via self-update + a CP config backfill → an operator-watched Docker→Colima migration.

## User Stories

1. As a field tech, I want to rack and power a new Mac and have it appear in the CP on its own, so that I never run a setup script on-site.
2. As a field tech, I want zero credentials or commands to type on-site, so that a deployment can't fail on a fat-fingered token.
3. As an MDM admin, I want Mosyle to deliver one signed pkg and trigger it, so that I don't maintain a fragile bootstrap script with embedded secrets.
4. As a security owner, I want the device install package to carry only a one-time bootstrap key, so that a stolen device or leaked package can't expose fleet-wide credentials.
5. As a security owner, I want each device to receive its own single-use Tailscale key, so that a compromised device can't enroll further tailnet nodes and key rotation isn't fleet-coupled.
6. As an operator, I want a newly-provisioned device to show up as "enrolled, unassigned," so that I can see devices awaiting assignment.
7. As an operator, I want to assign an unassigned device to a Site from the dashboard, so that the CP can commission it remotely.
8. As an operator, I want the CP to push cameras, ALPR config, the tailnet key, the service allow-list, and snapshot cadence on assignment, so that the device comes into service without anyone touching it.
9. As an operator, I want to run a `network.scan` and verify camera angles via Edge UI as part of commissioning, so that camera setup is remote.
10. As a staff admin, I want to enter the account-wide Plate Recognizer token once and a per-device license on the device, so that the CP can commission ALPR without an on-site config edit.
11. As an operator, I want ALPR to start only when I commission it, so that a per-device license is consumed only on activation.
12. As an operator, I want Colima + the ALPR container reported as services, so that I can see when the container runtime is down rather than silently losing plates.
13. As an installer of new Macs, I want the install to be safe to re-run, so that an interrupted provision can simply be re-triggered.
14. As an agent developer, I want install logic in typed, tested Go reusing the agent's CP client, so that there's one enrollment/config codebase instead of four drifting Bash copies.
15. As a release engineer, I want install bug-fixes to ride the existing self-update channel, so that fixing install doesn't mean re-touching every device.
16. As a release engineer, I want the pkg to carry only a bootstrapping binary, so that it's rebuilt rarely (key rotation / installer-logic change), not per agent release.
17. As an operator of the existing fleet, I want live devices to converge to the new model without re-installation, so that stores aren't disrupted.
18. As an operator, I want the pre-supervisor stragglers finished via `migrate-fleet.sh` first, so that the whole fleet is on the self-update channel.
19. As an operator, I want existing devices to receive the new config fields (e.g. `snapshot_state_path`) via a CP backfill, so that dormant features (scheduled snapshots) activate.
20. As an operator, I want the Docker→Colima migration of live ALPR devices to be operator-watched over SSH, so that swapping a running container runtime is done per-device with a human watching.
21. As a maintainer, I want the dead modules (Zabbix, AnyDesk, S3 registry, raven) and the Bash framework deleted once no new device depends on them, so that the repo stops carrying phased-out weight.
22. As an MDM admin, I want privacy grants (Microphone for Edge UI) handled by a Mosyle PPPC profile, so that the installer never tries to click a TCC prompt.
23. As a developer, I want the camera TCC permission *not* requested, so that we don't over-grant for RTSP cameras that need no local-camera access.
24. As an operator, I want auto-login health surfaced, so that I learn when a stuck login has taken Colima/ALPR down.
25. As an agent developer, I want host mutations behind the OS-agnostic backend split, so that the CP/API/dashboard stay OS-agnostic while macOS verbs live in the agent.
26. As a developer, I want install steps to be idempotent by inspecting real system state, so that a stale state-ledger can't drift from reality.

## Implementation Decisions

Module boundaries confirmed with the operator (2026-06-15). Device-side modules live in the agent codebase (`internal/agent/*`, `cmd/agent`); CP-side in `internal/cp/*`.

**Device-side (new Go, in the agent binary):**

- **`enroll`** — device-side enrollment client. Interface roughly: gather hardware identity (hardware UUID, hostname, OS version, agent version) → `POST /enrollments` with an `Idempotency-Key` of the hardware UUID → install cert/key/CA → write the typed agent-config. Reuses the agent's typed config structs. The CP-side handler already exists (`internal/cp/api/handlers/enrollment`); this is the missing *device* half (currently Bash `curl` in module 11).
- **`install`** — Provision orchestration as an ordered list of **idempotent-by-inspection steps**, each exposing `IsDone()` / `Apply()` over a system interface: Homebrew, the brew formulae (ffmpeg, tailscale, nmap, colima, docker CLI, whisper-cpp), pull the ALPR image, lay down agent/supervisor/edge-ui binaries + the LaunchDaemon, the Colima user LaunchAgent, then `enroll`. No separate state-file ledger.
- **`hostconfig`** (darwin backend, behind the ADR-034 `Backend` split) — SSH/ARD enable, auto-login `kcpassword`, `pmset`, hostname, `codesign` of ffmpeg/whisper. macOS verbs only behind the backend; a command-runner seam for testing.
- **`container`** — Colima + ALPR lifecycle across the uid boundary: ensure the per-user VM is up (install the user LaunchAgent), write `config.ini`, start/restart the container (`--restart=unless-stopped`), read logs — all via `launchctl asuser $(id -u uknomi) sudo -u uknomi …`. Resolves the same way for the `log.tail` `docker` kind.
- thin `cmd/agent` subcommands: `install` (one-shot Provision) and `migrate-colima` (existing-fleet runtime swap).

**CP-side (new Go):**

- **`tailscale`** — Tailscale API client minting an ephemeral, single-use, tagged auth key per device. Isolated behind a small interface; the CP holds a Tailscale API credential in Secrets Manager, used only by Commission.
- **`commission`** — orchestrates the Assign→Commission push: resolve the assigned site's config, mint the Tailscale key, gather the account PR token + per-device ALPR license, and fan out the cmds (`cameras.update` + a Commission config bundle) over the existing dispatcher. Secret-bearing messages **non-retained**; agent persists secrets `0600 root`. Authenticity hardens with Phase 3 envelope signing (ADR-028 → ADR-035).
- **Schema / API** — account-wide PR token stored once (settings); per-device ALPR license column on `devices` (staff-entered). A Commission trigger endpoint (or Assign-triggers-Commission); the enrolled-unassigned state already exists (NULL `site_id`). `snapshot_state_path` + any new agent-config fields added to the config the CP delivers.
- **Dashboard** — staff settings field for the PR token; per-device ALPR license field; an Assign→Commission action on the rollout surface; Colima/container service rows; auto-login health surfaced.

**`uknomi-edge-install` (new repo — github.com/emilejacobs/uknomi-edge-install — replacing the local-only `mac-mini-rollout`):**

- The **signed-pkg CI build** — package the agent binary (pulled from `control-plane`'s dist bucket) + baked bootstrap key; postinstall runs `uknomi-agent install`; Developer ID sign + notarize (shares the ADR-035 signing machinery).
- **`migrate-fleet.sh`** extension — drive the operator-watched Docker→Colima migration.
- **Decommission `mac-mini-rollout`** — ensure no new-device path depends on it; the dead modules (`04-zabbix`, `05-anydesk`, `10-s3-register`, `51-raven`), bootstrap scripts, `setup.sh` framework + `lib/*`, and `08`/`09` are simply not carried forward; `52-plate-recognizer` is superseded by the Colima path.

## Testing Decisions

A good test asserts **external behavior**, not implementation details — the observable result of a step, not the sequence of internal calls. Per [ADR-012](../../docs/adr/0012-test-policy.md), every CP endpoint gets an integration test and every mutating endpoint an idempotency test; these cover the Commission trigger and the PR-license PUT by policy.

Operator decision (2026-06-15): **unit-test all four device-side modules via fakes** — build a command-runner / system seam and a fake HTTP CP:

- **`enroll`** — against a fake CP HTTP server: success writes cert/key/CA + config; re-run with the same `Idempotency-Key` is safe; error responses surface cleanly.
- **`install`** — against a fake system: idempotency by inspection (a step whose `IsDone()` is true is skipped), correct step ordering and dependency (enroll only after binaries + config land), partial-run resumption.
- **`hostconfig`** — against a fake command runner: each mutation issues the expected verb and is a no-op when already applied.
- **`container`** — against a fake runner: container ops route through `launchctl asuser`; `config.ini` write + mount round-trip; service-status reporting reflects VM/container state.
- **`tailscale`** (CP) — against a fake Tailscale API: minted keys are single-use + tagged; API errors propagate.
- **`commission`** (CP) — the fan-out gathers the right config and pushes the expected cmds; secrets non-retained.

Prior art: the agent dispatcher/handler tests (`internal/handlers/*`), the enrollment handler tests, and ADR-035's wrapper/manifest test patterns (signing, promote/rollback). Integration prior art: `tests/integration/*` (e.g. `command_signing_test.go`, idempotency tests).

## Out of Scope

- **Phase 3 command signing** of the Commission cmds — rides the existing ADR-028 → ADR-035 path; not built here.
- **Linux/Pi/Radxa install** — Mac-only overhaul (fleet direction); the ADR-034 backend split keeps the door open but no Linux backend is written.
- **#10 audio** — the install overhaul provisions the runtime (whisper, Microphone PPPC) but the audio feature itself is separate, built on the now-merged generic uploader.
- **Mosyle PPPC profile authoring** — owned by Mosyle/MDM, out of the installer's scope; the installer assumes + verifies grants, never sets them.
- **The full Edge UI Talon→"uKnomi Edge" rename + Flask `09-webui` retirement** — tracked with the Edge UI rework (ADR-029/030/032); this PRD only removes the install module once Edge UI covers it.
- **Whisper model / ALPR image mirroring to the dist bucket** — a reliability nicety over HuggingFace/Docker Hub; deferred.

## Further Notes

- **Camera discovery is a Commission-time operator workflow** — `network.scan` (CP→agent) → operator builds the camera list → `cameras.update` → Edge UI "Verify angle." The installer does nothing camera-specific beyond installing ffmpeg + edge-ui.
- **Auto-login is load-bearing** — Colima's per-user VM (and thus ALPR) depends on the `uknomi` GUI session; surfaced as service health so a stuck login is visible, not silent.
- **The pkg is rebuilt rarely** — only on bootstrap-key rotation or installer-logic change; routine agent releases flow through self-update.
- **Two repos** — device + CP Go code in `uknomi-control-plane` (install logic folded into the agent binary, ADR-037); the pkg build + Mosyle/MDM assets + migration harness + runbooks in the new **`uknomi-edge-install`** repo (github.com/emilejacobs/uknomi-edge-install), which replaces the local-only `mac-mini-rollout`.
