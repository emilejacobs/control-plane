# ADR-007: Pi/Radxa minimal-agent only

**Status:** Accepted (2026-05-05)

**Context.** Linux fleet (Pis + Radxas) is being phased out — failed Pis are replaced with Mac Minis; no new Pi or Radxa rollouts. Linux devices run only three services: Zabbix, Raven, Tailscale.

**Decision.** The Linux build of the agent ships only the bare-minimum command set: `heartbeat`, `service.status`, `service.restart`, `run-signed-script`, `reboot`. No local-UI proxy, no Linux-specific features. Cross-compilation makes this nearly free.

**Consequences.**
- (+) Visibility on the legacy fleet during transition without investing in throwaway features.
- (+) The same Go agent codebase covers both OSes.
- (-) Linux operators don't get Edge UI features. Acceptable — Pis don't run an Edge UI.
