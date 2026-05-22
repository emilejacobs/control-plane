# Claude session notes

This repo is in **active implementation** as of 2026-05-21. The Phase 0 agent spike and Phase 1 issues #02–#04, #07, and #19 have landed — there is a Go codebase under `cmd/` and `internal/`. The design and decision documents under `docs/` still anchor the work; read them before changing anything.

## Before doing anything

1. Read `docs/CONTEXT.md` first for terminology. The glossary anchors the rest.
2. Read `docs/decisions.md` (the ADR index) and any ADRs under `docs/adr/` relevant to your task. The architecture is settled on a specific shape; don't re-litigate without explicit user direction.
3. Read `docs/architecture.md` for the system design.
4. The sister repo at `../mac-mini-rollout` contains the existing install scripts and local Edge UI (Flask app, formerly called "Talon", being renamed to "uKnomi Edge"). Read from it directly when reasoning about install flow or local services.

## What this project is

Centralized AWS-hosted control plane for managing ~63 edge devices (25 Macs + 36 Pis + 2 Radxas) at US client sites. Replaces per-device SSH/Tailscale access. Future mobile app for field operators is a deliberate design constraint, not a "maybe."

## Naming

- **CP** = Control Plane (this project, the AWS side)
- **Edge UI** = the device-local Flask app (was "Talon"). Lives in `mac-mini-rollout/webui/`.
- **Agent** = `uknomi-agent`, the Go binary on every device that talks to the CP.

## Don't

- Don't write code unless the task calls for it — an assigned issue or an explicit request. Many sessions here are still design, planning, or docs work.
- Don't add features outside the scope listed in `docs/architecture.md` § Goals/Non-goals.
- Don't propose Zabbix integrations — explicitly de-prioritized.
- Don't suggest investing in Pi/Radxa-specific features — those platforms are being phased out (failed Pis are replaced with Macs).

## Agent skills

### Issue tracker

Local markdown under `.scratch/<feature>/`; ephemeral (no remote tracker yet — GitHub may be added later). Promote durable decisions to ADRs rather than tracking them as issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`) plus a local `done` extension for completed issues we want to keep on disk. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context. Glossary at `docs/CONTEXT.md`; ADRs at `docs/adr/` indexed by `docs/decisions.md`. See `docs/agents/domain.md`.
