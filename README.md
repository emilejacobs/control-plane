# uKnomi Control Plane

Centralized AWS-hosted fleet management for uKnomi's edge devices. Replaces per-device SSH/Tailscale access for service control, log retrieval, and remote command execution across the fleet.

**Status:** Design phase, 2026-05-05. No code yet.

## What this is

uKnomi runs ~63 edge devices (25 Mac Minis, 36 Raspberry Pis, 2 Radxa Rocks) at client sites across the US. Each device runs services like the local Edge UI (formerly "Talon"), camera capture, transcriber, raven, and plate-recognizer. Today, managing the fleet means logging into each device individually over Tailscale.

The Control Plane (CP) consolidates this into a single web dashboard with a remote-command capability, signed-and-audited script execution, and proxied access to each device's local Edge UI. The system is built API-first so a future mobile app for field operators can be added without re-platforming.

## Documents

- [Architecture](docs/architecture.md) — system design, components, data flow, mobile readiness
- [Decisions](docs/decisions.md) — ADR index, linking to individual decisions under [`docs/adr/`](docs/adr/)
- [Roadmap](docs/roadmap.md) — phased delivery plan
- [Costs](docs/costs.md) — AWS infrastructure cost estimate
- [Domain context](docs/CONTEXT.md) — terms and naming

## Related repos

- `mac-mini-rollout` — Mac install scripts and the local Edge UI (Flask app being proxied by CP). Will gain a CP enrollment module (`11-cp-agent.sh`) in Phase 1.

## Reading order for circulation

1. This README (you are here).
2. [docs/architecture.md](docs/architecture.md) for the design.
3. [docs/decisions.md](docs/decisions.md) for the ADR index, then individual ADRs under [docs/adr/](docs/adr/) to see what was chosen and why.
4. [docs/roadmap.md](docs/roadmap.md) for delivery phasing.
5. [docs/costs.md](docs/costs.md) and [docs/CONTEXT.md](docs/CONTEXT.md) as reference.
