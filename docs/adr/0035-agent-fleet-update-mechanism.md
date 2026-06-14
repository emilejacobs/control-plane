# ADR-035: Agent fleet-update — push+reconcile delivery, resident-wrapper rollback, derived rollout

**Status:** Accepted (2026-05-29) — refines [ADR-013](./0013-agent-self-update-phase-3.md) (agent self-update + auto-rollback) and corrects its "Ed25519 in KMS" detail.

**Context.**

ADR-013 accepted agent self-update with auto-rollback as a Phase 3 deliverable and sketched a mechanism (signed S3 manifest, periodic poll + signed `agent.update` command, last-known-good snapshot, revert if no healthy heartbeat in 5 min). It left the load-bearing shape open.

The agent now runs on the whole Mac fleet, and the near-term backlog (snapshots, audio, Plate-Recognizer config, webhooks — #5/#6/#8–#10) each ships a new agent version. Manual `ssh`+`tar` per device per release is the emerging bottleneck. We want CP to push an update to the fleet or a **selected subset**, safely, without SSH. A design session (proposal in [`.scratch/agent-fleet-update/`](../../.scratch/agent-fleet-update/PROPOSAL.md)) resolved the open mechanism questions; this ADR records the load-bearing choices so they aren't silently re-litigated when the work is sliced.

Two existing facts shaped the result: the agent already receives commands via an MQTT **push** dispatcher (no poll loop), and CP already ingests **heartbeat version reports + lifecycle connect/disconnect** events — so CP always knows what version each device runs and the instant it reconnects.

Separately, ADR-013 says the manifest/command Ed25519 key lives "in KMS." AWS KMS does not offer Ed25519 signing keys (RSA + ECC-NIST/secp256k1 only); the detail is infeasible as written.

**Decision.**

1. **Delivery: push + CP-side reconcile (no agent poll loop).** CP stores a per-device **desired agent version**. An update sets desired on the target set and pushes `agent.update` immediately; CP **re-pushes** to any targeted device that reconnects still on the wrong version, driven by the lifecycle/heartbeat signals it already ingests. This keeps per-device targeting and canary *and* converges offline devices, without an agent-side poll loop or a fleet-global manifest poll (which would break targeting). The S3 manifest is the signed **catalog of available versions**, not the targeting mechanism.

2. **Signing — manifest now, command later.** The manifest carries an **Ed25519 signature** verified by the agent from the **first signed release** (slice 1). The keypair is offline: the **public** key is baked into the agent build (the ADR-017 bootstrap-key mechanism, simpler since it's public); CI signs the manifest at release with the private key (a CI secret); the agent verifies with `crypto/ed25519`. **No KMS** — release-time offline signing. The `agent.update` *command* may stay unsigned initially: with manifests signed, a forged command can at worst trigger a re-check or a move to another *validly-signed* version, closed by a no-downgrade (version-monotonic) rule. Command-envelope signing is deferred (`envelope.Command.Signature` already exists and composes per [ADR-028](./0028-unsigned-config-update-phase-2.md)); when built it uses an Ed25519 key in Secrets Manager (sign in-process) or KMS-ECDSA-P256 — **not** KMS-Ed25519.

3. **Rollback: a resident wrapper supervisor.** The LaunchDaemon/systemd `Program` is a thin resident **wrapper** that supervises the agent as a child. The agent downloads → verifies sha256 + manifest signature → stages a `candidate` → flags "trying" → requests restart, and after a healthy start writes a health marker. The wrapper owns the gate: run candidate → wait ≤5 min for the marker → **promote** (save prior as last-known-good) or **revert** to last-known-good and emit a `rollback` telemetry event. On-disk: `current` / `last-known-good` / `candidate`. The wrapper is OS-specific glue → it lives in the install repos (`mac-mini-rollout`, `scripts/install-cp-agent.sh`); CP and the agent stay OS-agnostic. A broken new binary never decides its own fate — the older, stable wrapper does, off an observable signal.

4. **No campaign entity — derive the rollout.** Per-device desired agent version is the whole model. Targeting = set desired on a device set; canary = set on a subset, watch, then the rest; rollout view = desired-vs-reported across the fleet; abort = reset desired to current for un-converged devices; each "set desired" is audit-logged (the rollout record). No campaign table/state-machine until auto-ring-promotion or scheduled rollouts are genuinely wanted.

5. **Health gate = alive + controllable.** The marker is written once the new binary has (a) established the mTLS IoT connection, (b) **subscribed to its cmd topic** (so it stays commandable — the next fix can be pushed), and (c) published one heartbeat (~30–60s). It does **not** require probe/service-status reports: those are 5-min-cadence + optional, their failures aren't catastrophic, and a forward fix beats an auto-rollback for them. The gate guards the catastrophic *unreachable/uncontrollable* failure only.

**Consequences.**

- (+) Updates become a CP operation over the existing push channel + version telemetry — no SSH, no new agent poll loop, no second daemon.
- (+) Manifest signing from the first release closes the worst window: there is never a moment where the fleet trusts an unsigned binary, and the public-key bake reuses a proven pattern.
- (+) "Derive, no campaign entity" keeps slice 1–2 free of an orchestration state-machine; rollout visibility falls out of the version drift CP already shows.
- (+) The resident wrapper makes rollback robust against a binary too broken to roll itself back.
- (−) A **one-time manual install** is unavoidable: the wrapper + the baked-in public key must land via the current (manual) install before self-update can be trusted. This is the last manual rollout.
- (−) The reconcile logic adds CP-side state (per-device desired version + a re-push-on-reconnect path) and an audit surface.
- (−) Auto canary→ring promotion and scheduled rollouts are out until a campaign entity is added later; v1 canary is manual (subset, watch, rest).
- (−) Command-channel authenticity for `agent.update` lags manifest authenticity until slice 2; mitigated by the signed manifest + no-downgrade rule.

**Verification.** TBD at implementation. Tests cover: manifest signature + sha256 verification (good/forged/downgrade), the wrapper promote/rollback cycle against a deliberately-broken candidate, the health-marker gate (alive+controllable, not feature reports), and CP reconcile re-pushing on reconnect when reported ≠ desired.

**Command signing — landed (#41, slice 2).** `agent.update` envelope signing is implemented per decision #2: CP signs the command in-process with an Ed25519 private key held in **Secrets Manager** (NOT KMS — KMS has no Ed25519); the agent verifies against a **baked-in public key** (`internal/protocol/cmdsign`, mirroring the manifest key but distinct and *online*) before dispatch, and refuses any **downgrade** (`agentupdate.IsDowngrade`) even under a valid signature. The dispatcher gates only the high-blast-radius `agent.update`; the Phase 0/2 handlers stay unsigned (ADR-028 forward-compat).

*Key handling.* Two distinct keypairs, opposite availability:
- **Manifest key** (#38): private half is the CI secret `AGENT_MANIFEST_SIGNING_KEY`, never in AWS; public half `internal/protocol/agentmanifest/release_pubkey.b64`.
- **Command key** (#41): private half in Secrets Manager, loaded by cp-api + cp-ingest via `CP_COMMAND_SIGNING_SECRET_ID`; public half `internal/protocol/cmdsign/command_pubkey.b64`.

Both committed public keys are **DEV keys** — rotate before production: regenerate with `cmd/agent-manifest-keygen`, commit the new public key, set the new secret. Tests: `cmdsign` sign/verify + wire round trip, dispatcher gate (missing/invalid/forward-compat), no-downgrade rule, and a CP→agent end-to-end signing contract (`tests/integration/command_signing_test.go`).
