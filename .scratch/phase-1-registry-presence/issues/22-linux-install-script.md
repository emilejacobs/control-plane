# Issue 22 — One-page Linux install script

Status: done
Type: AFK
Completed: 2026-05-23 — 7-cycle TDD slice; rollout (Wave 3, #23) remains operator-gated.

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Story 29, § Deliverables.
- ADR: ADR-007 (Pi/Radxa minimal agent — these platforms are deprecating).
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — "One-page Linux install script for Pi/Radxa enrollment (built, but rollout deferred — see below)."

## What to build

The Linux equivalent of `mac-mini-rollout/modules/11-cp-agent.sh` (#11), shaped to honor the "one-page" constraint settled in #02. Built in Phase 1 so the path exists; actual rollout (Wave 3, #23) runs as a parallel track that does not gate Phase 1 exit.

Scope:

- A single shell script (the "one-page" constraint's exact form — LOC ceiling? single file? — is settled in #02). Probably something like `install-cp-agent.sh`, hosted via S3 + presigned URL for download.
- Reads hardware identifiers from the Linux device (machine-id, hostname, OS via `os-release`).
- POSTs to `<CP base URL>/enrollments` with the bootstrap key baked in at script-build time (same Secrets-Manager-→-CI flow as #10, adapted for the Linux script).
- Writes the cert + key to `/etc/uknomi/`, mode 0600.
- Creates a systemd unit at `/etc/systemd/system/uknomi-agent.service` and enables + starts it.
- Idempotent on re-run: detects existing enrollment (via `Idempotency-Key: <machine-id>`) and just reinstalls the daemon if needed.

## Acceptance criteria

- [x] **Size/shape.** "One-page" was never formalized in #02 (it landed as a "branches still to grill" PRD note, not a TBD #02 closed). Resolved during this issue: single file, single language (bash + small Python heredocs for JSON), < 250 LOC. Final: **162 LOC**, shellcheck-clean at `--severity=warning`. Tracked by `TestInstallScriptIsShellcheckClean` + the new `shellcheck` job in `.github/workflows/ci.yml`.
- [ ] **On a clean Pi or Radxa with internet access, running the script enrolls the device and starts the agent.** Deferred to Wave 3 (#23, `ready-for-human`). The simulator-side test exercises every code path through a sandboxed root + a fake CP that returns the canonical enrollment response; the on-hardware run is a Wave-3 checklist item.
- [ ] **The agent connects to IoT Core within 30s and publishes its first heartbeat.** Same deferral — requires real hardware + a deployed CP.
- [x] **Re-running the script on the same device is idempotent.** `TestInstallScriptReRunIsIdempotent` runs the script twice against the same sandbox; both runs succeed, server-side `Idempotency-Key` replays the canonical response, on-disk state converges.
- [x] **The bootstrap key is not visible in any log produced by the script.** `TestInstallScriptKeepsBootstrapKeyOutOfLogs` runs the script with `bash -x` (trace mode echoes every command + expansion to stderr) and scans the combined output for the key sentinel. The key only ever exists inside the Python heredoc, never in a shell variable or argv.
- [x] **Documentation updated.** [`docs/architecture.md`](../../../docs/architecture.md) § Enrollment flow now describes the Linux script's shape + paths + LOC; CONTEXT.md untouched (no new domain terms — Linux install script is described by ADR-007 + ADR-017); no new ADR (the "one-page" interpretation is a soft constraint documented in this issue, not a load-bearing decision worth a separate ADR).

## Blocked by

- Issue 02 ("one-page" constraint resolution). ✅ Resolved in-line above: < 250 LOC, single file, shellcheck-clean.
- Issue 03 (enrollment endpoint). ✅ Done before this slice.
- Issue 10 (bootstrap key + Secrets Manager flow). ✅ Done before this slice. The CI flow that bakes the bootstrap key into the Linux script's distribution is filed as a follow-up note below — the script reads it from `CP_BOOTSTRAP_KEY_FILE`; the question is just *who writes that file*.

### Completion notes (2026-05-23)

7 cycles, `11c1263` → this commit:

1. `11c1263` — TDD tracer: script POSTs to `<CP>/enrollments` with `Idempotency-Key: <machine-id>`. Test harness in `tests/installscript/` sandboxes a fake `/etc` + a stubbed `systemctl` on PATH + an httptest fake CP.
2. `161c1cd` — TDD: cert.pem + key.pem at 0600, agent-config.json at 0644 under `${ROOT}/etc/uknomi/`. Python heredoc parses the response, never lets the cert PEM enter argv.
3. `1aae6fc` — TDD: agent binary at `/usr/local/bin/uknomi-agent`, systemd unit at `/etc/systemd/system/uknomi-agent.service`, `systemctl daemon-reload → enable → restart`. Test asserts the unit content + the systemctl-stub call log.
4. `086f8e5` — Lock the existing idempotency posture: re-running succeeds, server replays. No script change.
5. `f8e1845` — `bash -x` trace-mode regression test for the key-not-in-logs property.
6. `1908564` — Shellcheck gate. New `shellcheck` job in `ci.yml` (apt-get install on ubuntu-latest, fails on `--severity=warning`); local Go test mirrors but skips if shellcheck absent. Final script: 162 LOC, shellcheck-clean.
7. This commit — `architecture.md` updated, issue closed, follow-ups below.

### Follow-ups (not blocking; file when needed)

- **CI flow that bakes `CP_BOOTSTRAP_KEY_FILE` into the published script.** The Mac module bakes the key at install-package build time via a Secrets-Manager-fetch step in the `mac-mini-rollout` CI. The Linux equivalent (publishing `install-cp-agent.sh` + the bootstrap key to S3 with a presigned URL) does not exist yet. Wave 3 (#23) needs this — the script currently expects an environment variable pointing at the key file, which is fine for the dev/test path but is not the rollout path. File a `#29 — Linux install package CI` when Wave 3 starts.
- **Agent binary distribution.** The script expects `CP_AGENT_BIN_SRC` to point at a local file. The production rollout will need either a `--agent-url` flag (the script downloads from S3 at run time) or the script + binary shipped together as a tarball. Same Wave-3-timed concern as the key flow.
