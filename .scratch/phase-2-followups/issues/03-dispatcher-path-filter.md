# Issue 03 — Add `internal/dispatcher/**` to the agent's path filter

Status: ready-for-agent
Type: AFK
Estimate: 5 min

## Parent

- Source: Slice 2 cycle 9 added `Type` to `envelope.Result` and edited `internal/dispatcher/dispatcher.go` to populate it. The change rebuilt cp-api + cp-ingest because both filters cover `internal/envelope/**` (added in commit `099dd7f`). But the **agent** binary's release flow (currently manual via mac-mini-rollout staging) has no equivalent path filter — and even if it did, `internal/dispatcher/**` isn't in any filter set today.
- ADRs to honour: ADR-027 (auto-deploy semantics) — though the agent doesn't auto-deploy, the path-filter discipline still applies the moment an agent release pipeline lands.

## What to build

Trivial: add `internal/dispatcher/**` to wherever the agent's build artifacts get tracked. For now, the agent has no GHA workflow; the binary is hand-built and scp'd to bench Macs. So this issue is really a **note for whoever lands the agent's release pipeline** (Phase 3's self-update primitive needs one).

### Concrete actions

When the agent's release workflow is built, ensure the path filter covers:
- `internal/dispatcher/**`
- `internal/envelope/**`
- `internal/handlers/**`
- `internal/agent/**`
- `internal/telemetry/**`
- `internal/transport/**`
- `internal/config/**`
- `internal/service/**`
- `internal/protocol/**`
- `cmd/agent/**`
- `go.mod`, `go.sum`

Until then: when in doubt about whether a change affected the agent, **rebuild and re-stage** the agent binary into `mac-mini-rollout/bin/` and re-hot-swap any production agents that need it.

## Acceptance criteria

- [ ] One of: (a) the agent release pipeline lands with the full filter set above, OR (b) the agent's build-and-stage script lives somewhere obvious and a CLAUDE.md note tells future agents to run it after touching any of those paths.

## Blocked by

- The agent release pipeline. Without one, this is just discipline.

## Why park this

It's a real coordination cost (slice 2 surfaced it: cycle 5 wired `ConfigPath` into `agent.Config` — a change that mattered to the agent binary but didn't touch the CP path filters at all). The right fix is structural (the agent release pipeline) but until that lands, the watchlist entry in the followups README plus this issue is the documentation.
