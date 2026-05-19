# ADR-013: Agent self-update in Phase 3 with auto-rollback; Phase 1 cert TTL = 1 year

**Status:** Accepted (2026-05-18)

**Context.** The original roadmap placed both agent self-update and cert rotation in Phase 4 ("ongoing, 4–8 weeks spread over a quarter"). Two problems:

1. Phase 3 ships signed-command + `run-script` primitives. After Phase 3, ad-hoc agent updates become possible via a signed run-script that fetches a binary and swaps it. Without a first-class self-update flow with rollback, such updates can brick devices — a buggy new binary that fails to connect cannot be reverted without SSH, which defeats the project.
2. Phase 1 ships long-lived certs without specifying TTL. A long TTL (5+ years) is a known-bad pattern; a short TTL with no Phase 4 deadline creates risk of fleet-wide cert expiry before rotation lands.

**Decision.**

- **Agent self-update is a Phase 3 deliverable.** Mechanism:
  - A signed manifest in S3 specifies the current binary URL, version, and Ed25519 signature.
  - The agent polls the manifest periodically and on receipt of a signed `agent.update` command.
  - Before swapping, the agent records a "last-known-good" snapshot (binary + version).
  - After swap and restart, if the agent does not publish a successful heartbeat within 5 minutes, the supervising LaunchDaemon/systemd wrapper reverts to the last-known-good binary and emits a `rollback` telemetry event.
- **Phase 1 cert TTL = 1 year.** Long enough to ship Phase 3 + Phase 4 cert rotation; short enough to surface a deadline if rotation slips.
- **Cert rotation itself remains in Phase 4.** Once self-update exists, rotation is "push a new cert via signed command and reload" — a small additional deliverable on top of the self-update primitive.

**Consequences.**
- (+) The agent can be iterated safely after Phase 3 — no SSH-only fallback, no Mosyle re-trigger required for routine updates.
- (+) Auto-rollback removes the worst failure mode of self-update (devices unreachable after a bad ship).
- (+) The 1-year cert TTL creates a visible deadline that prevents rotation work from being silently deferred forever.
- (-) Phase 3 scope grows by ~1–2 weeks of agent + S3 manifest + rollback work.
- (-) Auto-rollback adds complexity to the LaunchDaemon/systemd supervision (need a supervisor that detects "new binary failed to heartbeat" and reverts).
- (-) If Phase 4 cert rotation slips past month 10, fleet-wide cert expiry becomes an emergent deadline.

**Verification.** TBD — added at implementation. Tests cover: signed-manifest verification, version comparison, rollback trigger after a deliberately-broken binary, telemetry of rollback events. An end-to-end test exercises the full update + rollback cycle.
