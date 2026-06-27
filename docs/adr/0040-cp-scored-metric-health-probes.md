# ADR-040: CP-scored continuous-metric health probes, with operator-tunable thresholds

**Status:** Accepted (2026-06-27)

**Context.** The fleet-health-probes design ([`.scratch/phase-2-fleet-health-probes/PRD.md`](../../.scratch/phase-2-fleet-health-probes/PRD.md), issue #19) made the **agent own the red/yellow/green scoring** — CP stores and aggregates a probe's status but does not recompute it (noted in `internal/protocol/healthprobes/types.go`). That was the right call for the original probes, which are all **boolean facts**: "auto-login configured?", "GUI at the login window?", "ALPR container running?". The agent observes the fact and the verdict is inherent — there is nothing to tune.

The 2026-06-26 store13/mesa ephemeral-port-exhaustion incident (see memory `agent_supervisor_orphan_candidate`, `colima_lan_camera_networking`) created the need for a different shape of probe: **host network pressure** — distinct ephemeral ports in use as a % of the pool, plus the CLOSE_WAIT count. Unlike a boolean, the verdict is a **threshold on a continuous metric**, and the operators explicitly want to adjust that threshold without shipping a new agent to ~24 devices each time. A fleet capture calibrated sensible defaults (healthy stores ~0.3% pool; mesa wedged at 82%), but "sensible today" must stay editable.

Two ways to make the threshold tunable:
1. **Agent-side scoring, thresholds pushed as config.** Honours the PRD rule verbatim. But host-pressure thresholds are *fleet-global policy*, while the agent-config push path ([ADR-028](./0028-unsigned-config-update-phase-2.md)/[ADR-036](./0036-cp-driven-device-lifecycle.md)) is *per-device* and applies on restart — so a tweak means pushing to every device and restarting agents. Slow and operationally heavy for what is one number.
2. **CP-side scoring at ingest.** The agent reports the raw metrics; cp-ingest applies the thresholds (read from `cp_settings`) when it persists the probe. A tweak in Settings takes effect on the next report (~5 min), fleet-wide, no agent involvement.

**Decision.**

For **continuous-metric** health probes, **CP owns the scoring**, against operator-tunable thresholds. Boolean probes are unchanged — the agent still scores them (the PRD rule stands for that category). Concretely, for `host_net_pressure`:

- The **agent reports raw metrics** in `Result.Details` (`ephemeral_pct`, `ephemeral_ports_used`, `pool_size`, `time_wait`, `close_wait`) and stamps a default-threshold status only for self-consistency.
- **cp-ingest re-scores** the probe from the raw Details against thresholds read fresh from `cp_settings` on every report, overriding the agent's status before persistence. Everything downstream is unchanged: the stored `status='red'` drives the existing `UnhealthyProbeRed` alert path ([ADR-039](./0039-outbound-fleet-notifications.md)) and the device-page `HealthPanel`.
- **Thresholds live in `cp_settings`** (`host_pressure.ephemeral_warn_pct` = 40, `.ephemeral_crit_pct` = 60, `.close_wait_warn` = 100, `.close_wait_crit` = 400) and are edited via staff-only `GET/PUT /settings/host-pressure`. Each field falls back independently to the calibrated default.
- The **scoring function and default constants live once** in `internal/protocol/healthprobes` (`EvalHostPressure`, `DefaultHostPressureThresholds`), used by both the agent (defaults) and cp-ingest (live settings), so the two halves cannot drift.
- **green < warn ≤ yellow < crit ≤ red**, and the probe takes the **worse** of its two signals. Yellow is dashboard-only; **red pages** — so the critical line is the alert threshold.

This does **not** weaken [ADR-034](./0034-agent-backend-abstraction-os-agnostic-surface.md): the metrics CP scores on are OS-agnostic numbers, and no OS-specific verb (`netstat`, `sysctl`) crosses into CP — the agent backend still does all the measuring.

**Consequences.**
- (+) Operators retune the red/yellow line in Settings and it applies fleet-wide within one probe interval — no agent roll, no restart, the original requirement.
- (+) The alerting, storage, and dashboard pipelines are reused as-is; CP-side scoring is a single re-score step at ingest.
- (+) Raw metrics are persisted in `details_jsonb`, so a threshold change re-colours future reports and the history stays interpretable.
- (−) The scoring rule for metric probes now lives in two conceptual places (agent default vs CP authoritative). Mitigated by the single shared `EvalHostPressure` + defaults; the agent's status is explicitly non-authoritative.
- (−) A second probe *category* with different ownership is a subtlety a contributor must learn — hence this ADR. The rule: **boolean ⇒ agent scores; tunable metric ⇒ CP scores from `cp_settings`.**
- (−) cp-ingest reads settings per report. Volume is tiny (~24 devices × 5 min), so uncached is fine; revisit if metric probes proliferate.

**Verification.**
- `EvalHostPressure` unit tests cover green/yellow/red on each signal and worse-of-two (`internal/protocol/healthprobes/hostpressure_test.go`).
- An ingester test asserts CP overrides the agent's status from Details, and a second asserts a configured threshold source moves the line (`internal/cp/ingest/healthprobes_test.go`).
- Settings handler tests cover default-fill on GET and `warn < crit` validation on PUT.
- The probe parse + Details round-trip are unit-tested so the agent-write / CP-read contract can't drift.
