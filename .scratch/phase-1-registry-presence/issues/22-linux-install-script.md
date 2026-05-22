# Issue 22 — One-page Linux install script

Status: ready-for-agent
Type: AFK

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

- [ ] The script honors the size/shape constraint settled in #02.
- [ ] On a clean Pi or Radxa with internet access, running the script enrolls the device and starts the agent.
- [ ] The agent connects to IoT Core within 30s and publishes its first heartbeat.
- [ ] Re-running the script on the same device is idempotent.
- [ ] The bootstrap key is not visible in any log produced by the script.
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 02 ("one-page" constraint resolution).
- Issue 03 (enrollment endpoint).
- Issue 10 (bootstrap key + Secrets Manager flow extended for Linux script).
