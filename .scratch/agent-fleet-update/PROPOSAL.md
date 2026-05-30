# Proposal — Agent fleet update (push agent versions from CP)

**Status:** Draft for discussion — 2026-05-29
**Relates to:** ADR-013 (agent self-update + auto-rollback, accepted), ADR-028 (unsigned cmd channel carve-out)

## Problem

The agent now runs on all ~25 Macs (heading to ~63). The near-term backlog
(#5 PR-config, #6 webhooks, #8–#10 snapshots/audio) each ships a new agent
version. Today the only way to update is manual — `ssh` + `tar` + run the
install script, per device. That's O(devices × releases) toil, error-prone,
and will be the bottleneck on every feature from here on.

We want CP to **push an agent update to the whole fleet or a selected subset**,
safely, without SSH.

## Goals

- Operator triggers an agent update to a **target set** (all, by site/client,
  or an explicit device list) from CP.
- The update is **safe**: a bad binary that can't reconnect **auto-reverts**
  to the last-known-good without human intervention (ADR-013's core
  requirement — without this, self-update can brick the fleet).
- CP shows **rollout state**: which devices are on which version, in-flight,
  succeeded, or rolled back. (We already report `agent_version` per device.)

## Non-goals (for the first cut)

- Cert rotation (ADR-013 keeps it in Phase 4; it rides this primitive later).
- Arbitrary `run-script` / remote shell (separate Phase 3 primitive).
- Auto-update on a schedule — updates are operator-initiated; polling is only
  the delivery mechanism, not an auto-upgrade policy.

## What already exists (we're not starting from zero)

- **Version visibility** — agents report `agent_version` via heartbeat →
  `devices.agent_version`; the Overview "version drift" KPI already surfaces it.
- **Downward cmd channel** — `config.update` / `log.tail` / `network.scan` /
  `cameras.update` are built end-to-end over `devices/{id}/cmd` (+ `cmd-result`
  ingest). An `agent.update` cmd rides the same rail.
- **`agent-dist` S3 bucket** — already provisioned by Terraform for exactly this.
- **The design is decided** — ADR-013 specifies signed S3 manifest + poll +
  `agent.update` cmd + last-known-good snapshot + 5-min-heartbeat rollback.

## Proposed approach (per ADR-013)

1. **Manifest in S3 (`agent-dist`)** — `{version, url, sha256, signature}`.
   CI publishes the built binary + manifest on release.
2. **Agent updater** — on an `agent.update` cmd (push), the agent downloads →
   verifies sha256 + Ed25519 manifest signature → stages a `candidate` → flags
   "trying" → requests restart. (No agent poll loop — see decision #2.)
3. **Rollback supervisor** (resident wrapper in the install repos) — gates the
   candidate on the health marker (decision #5) within 5 min; promotes or
   reverts to last-known-good and emits a `rollback` telemetry event. **The
   load-bearing safety piece** — decision #3.
4. **CP-side rollout** — an update sets the per-device **desired agent version**
   on the target set and pushes `agent.update`; CP re-pushes to targeted devices
   that reconnect on the wrong version. Rollout state is *derived* (desired vs
   reported) — no campaign entity (decision #4). Canary = set desired on a
   subset, watch convergence, then the rest.

## Delivery slices (safety before speed)

1. **Safety spine** — S3 manifest + agent updater + rollback supervisor, driven
   by a single-device `agent.update`. **The manifest is Ed25519-signed from day
   one** (see "Signing", below) — this is the actual fleet-takeover guard and
   is cheap now but risky to retrofit. The `agent.update` *command* may stay
   unsigned here. *This slice must be rock-solid; the rest is UX.*
2. **Command signing** — populate the existing `envelope.Command.Signature`
   for `agent.update` (and the other cmds) as part of the Phase 3 signed
   envelope; closes the ADR-028 carve-out.
3. **Targeting + rollout UI** — device targeting + canary, and a
   desired-vs-reported rollout view in the dashboard (no campaign entity — see
   decision #4).

## Signing — two surfaces, resolved

"Signing" splits into two surfaces with opposite retrofit profiles:

- **Manifest signature** (authorizes "this binary is legit") — *the trust
  anchor*. **Build it into slice 1.** Effort is ~a day: generate an Ed25519
  keypair; bake the **public** key into the agent build (same mechanism as the
  ADR-017 bootstrap key, and simpler since it's public); CI signs the manifest
  at release with the private key (a CI secret); the agent verifies with
  `crypto/ed25519` (stdlib). No KMS — it's offline, release-time signing.
  Retrofitting later is expensive *and* risky: getting the public key onto
  already-deployed agents needs an update mechanism (the thing being secured),
  so the first "turn on signing" push goes out unsigned — a real attack window
  — and until then every update is checksum-only (integrity, not authenticity).

- **Command signature** (authenticates the `agent.update` cmd over IoT) —
  **defer to slice 2.** Cost is ~the same now or later and it's *designed to
  compose*: `envelope.Command.Signature` already exists (ships `nil` today;
  ADR-028 says Phase 3 wraps the same envelope without rewriting handlers).
  With manifests signed, an unsigned `agent.update` cmd is low-risk — a forged
  cmd can at worst trigger a manifest re-check or a move to another
  *validly-signed* version; a no-downgrade (version-monotonic) rule closes the
  residual downgrade risk.

**Correction to ADR-013/architecture.md:** they say "Ed25519 key in KMS," but
KMS doesn't offer Ed25519 (RSA + ECC-NIST/secp256k1 only). The manifest path
avoids KMS entirely (offline release-time signing). For command signing
(slice 2) we'd keep an Ed25519 key in Secrets Manager and sign in-process, or
use KMS-ECDSA-P256 for that surface. Neither blocks slice 1.

## Resolved design (grill session, 2026-05-29)

1. **Signing** — *resolved* (see "Signing — two surfaces"): manifest-signed in
   slice 1 (the trust anchor, cheap now, risky to retrofit); command-signed in
   slice 2 (designed to compose, low retrofit cost).

2. **Delivery — push + CP-side reconcile** (no agent poll loop). CP stores a
   per-device **desired agent version**; an update sets desired on the target
   set and pushes `agent.update` now. CP **re-pushes** to any targeted device
   that reconnects still on the wrong version, driven by the lifecycle/heartbeat
   signals it already ingests. Keeps targeting + canary *and* converges offline
   devices, without an agent-side poll loop or a fleet-global manifest poll
   (which would break targeting). The S3 manifest is the signed *catalog of
   available versions*, not the targeting mechanism.

3. **Rollback supervisor — a resident wrapper** that the LaunchDaemon/systemd
   `Program` execs in place of the agent, supervising the agent as a child. The
   agent downloads + verifies + stages a `candidate`, sets a "trying" flag, and
   requests restart; after a healthy start it writes a health marker. The
   wrapper owns the gate: run candidate → wait ≤5 min for the marker → **promote**
   (save prior as `last-known-good`) or **revert** to last-known-good and emit a
   `rollback` telemetry event. The wrapper is tiny, rarely changes, OS-specific →
   it lives in the install repos (`mac-mini-rollout` module 11 +
   `scripts/install-cp-agent.sh`); CP/agent stay OS-agnostic. A broken new binary
   never decides its own fate — the older, stable wrapper does, off an observable
   signal. On-disk layout: `current` / `last-known-good` / `candidate`.
   **One-time cost:** the wrapper + the baked-in manifest public key must land via
   one last manual install before self-update can be trusted.

4. **No campaign entity — derive from `desired_agent_version`.** The per-device
   desired version is the whole model. Targeting = set desired on a device set;
   canary = set on a subset, watch, then the rest; rollout view = desired-vs-
   reported across the fleet; abort = reset desired to current for un-converged
   devices; each "set desired" is audit-logged (that's the rollout record). No
   campaign table/state-machine until auto-ring-promotion or scheduled rollouts
   are genuinely wanted (a later slice).

5. **Health gate = alive + controllable.** The marker is written once the new
   binary has (a) established the mTLS IoT connection, (b) **subscribed to its
   cmd topic** (so it stays commandable — the next fix can be pushed), and (c)
   published one heartbeat — typically within ~30–60s. It does **not** require
   probe/service-status reports: those are 5-min-cadence + optional, their
   failures aren't catastrophic, and a forward fix beats an auto-rollback for
   them. The gate guards the catastrophic *unreachable/uncontrollable* failure.

## Risks / cost

- **Rollback complexity** is real and non-optional — it's the difference
  between safe and bricking. Most of the engineering risk is here.
- **Spans 3 repos** (agent, `mac-mini-rollout` supervisor, CP) + Terraform/CI
  for manifest publishing.
- ADR-013 ballparked the core at ~1–2 weeks; the targeting UI is on top.

## Recommendation

Worthwhile, and worth pulling forward from "Phase 3 someday" given the feature
cadence. Build the **safety spine first** (slice 1) so updates are *possible
and reversible*, then layer signing and the rollout UI. The design decisions
are settled (grill, 2026-05-29).

## Next step

`to-prd` / `to-issues` to slice this into tickets. An ADR (amending or
refining ADR-013) is warranted for the load-bearing choices — push +
per-device `desired_agent_version` reconcile (not agent poll), the
resident-wrapper supervisor, and "derive, no campaign entity" — since a future
reader will ask why, they're real trade-offs, and they're costly to reverse.
