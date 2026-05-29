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
2. **Agent updater** — on a signed `agent.update` cmd (and/or periodic poll),
   the agent: downloads → verifies checksum/signature → records last-known-good
   (binary + version) → atomic-swaps → restarts.
3. **Rollback supervisor** (in `mac-mini-rollout`, the LaunchDaemon wrapper) —
   if the new binary doesn't publish a healthy heartbeat within 5 min, revert
   to last-known-good and emit a `rollback` telemetry event. **This is the
   load-bearing safety piece.**
4. **CP "update campaign"** — operator picks a target version + device set;
   CP publishes `agent.update` to each, tracks per-device state
   (`pending → updating → done | rolled_back`) by watching the reported
   version + heartbeat, and renders a rollout view. Staged/canary = update a
   few, watch, then ring out.

## Delivery slices (safety before speed)

1. **Safety spine** — S3 manifest + agent updater + rollback supervisor, driven
   by a single-device `agent.update`. **The manifest is Ed25519-signed from day
   one** (see "Signing", below) — this is the actual fleet-takeover guard and
   is cheap now but risky to retrofit. The `agent.update` *command* may stay
   unsigned here. *This slice must be rock-solid; the rest is UX.*
2. **Command signing** — populate the existing `envelope.Command.Signature`
   for `agent.update` (and the other cmds) as part of the Phase 3 signed
   envelope; closes the ADR-028 carve-out.
3. **Targeting + rollout UI** — campaign model, device targeting, canary/ring
   rollout, per-device status view in the dashboard.

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

## Key open decisions (for the design session)

1. ~~**Signed now, or checksum-interim?**~~ **Resolved** (see "Signing"):
   manifest-signed in slice 1 (cheap, the real trust anchor, risky to retrofit);
   command-signed in slice 2 (designed to compose, low retrofit cost).
2. **Delivery: cmd-push, manifest-poll, or both?** Push gives immediate,
   targeted control; poll is the resilient fallback for offline devices.
3. **Where does the supervisor live + how does it revert?** New LaunchDaemon
   wrapper vs. teaching the existing install module; how last-known-good is
   stored on disk.
4. **Campaign model** — is "update campaign" a first-class CP entity (table +
   state machine) or just per-device cmd fan-out with status derived from
   reported version? Affects targeting/rollout-view scope.
5. **Health gate definition** — "healthy heartbeat in 5 min" vs. also requiring
   a successful probe/service report before declaring success.

## Risks / cost

- **Rollback complexity** is real and non-optional — it's the difference
  between safe and bricking. Most of the engineering risk is here.
- **Spans 3 repos** (agent, `mac-mini-rollout` supervisor, CP) + Terraform/CI
  for manifest publishing.
- ADR-013 ballparked the core at ~1–2 weeks; the targeting UI is on top.

## Recommendation

Worthwhile, and worth pulling forward from "Phase 3 someday" given the feature
cadence. Build the **safety spine first** (slice 1) so updates are *possible
and reversible*, then layer signing and the rollout UI. Resolve the five open
decisions in a short design session before writing code.

## Next step

A `grill-with-docs` session to settle the open decisions against ADR-013/028,
then `to-prd` / `to-issues` to slice it.
