# ADR-008: Skip Zabbix integration

**Status:** Accepted (2026-05-05)

**Context.** Zabbix is installed on devices today (`mac-mini-rollout/modules/04-zabbix.sh`). It's not used thoroughly, and the team is not investing in it.

**Decision.** Zabbix has no role in the CP design. No Zabbix-alerts-into-CP webhook, no Zabbix metric ingestion. Agent telemetry covers CP's needs.

**Consequences.**
- (+) One fewer integration to build, monitor, and document.
- (-) Existing Zabbix data isn't surfaced in CP. Acceptable — it wasn't being used.
- (-) `04-zabbix.sh` becomes increasingly dead weight in new rollouts. Removing it is a `mac-mini-rollout` decision, out of scope here, but worth raising in Phase 4.
