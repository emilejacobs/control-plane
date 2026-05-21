# Phase 1 — Registry, presence, and enrollment

Scratch directory for Phase 1 work. The phase itself is defined in [`docs/roadmap.md` § Phase 1](../../docs/roadmap.md). Estimate: 4–6 weeks.

## Objective (from roadmap)

Replace the device spreadsheet (`uknomi-macmini-devices.xlsx`) with a live online/offline view of the whole fleet.

## Deliverables (from roadmap)

- AWS infra in Terraform/CDK: VPC, ALB, RDS Postgres (multi-AZ), Fargate cluster, IoT Core CA, S3 buckets, KMS keys, Tailscale subnet router task.
- API service skeleton with auth (NextAuth Entra ID + Credentials — superseded by ADR-010, now local credentials only), enrollment endpoints, device list/detail endpoints, WebSocket for live updates.
- Next.js dashboard: login, fleet view (online/offline, by client/site), per-device view (read-only).
- New `mac-mini-rollout/modules/11-cp-agent.sh` that installs the agent and enrolls.
- One-page Linux install script for Pi/Radxa enrollment.
- Roll out to all 63 devices, read-only.

## Where this PRD-vs-issues split lives

Phase 0's PRD (`.scratch/phase-0-agent-spike/PRD.md`) anchored a single tight scope. Phase 1 is broader. We're running **issues-led, PRD-emergent**:

- Issue 01 (Terraform IoT Core codification) is genuinely independent infra work and runs now.
- The Phase 1 PRD lives at [`PRD.md`](./PRD.md) — first draft landed 2026-05-21 from a grilling session, with explicit TBD sections for branches not yet grilled (schema migrations, CI/CD, Linux one-page constraint, cost reconcile, observability beyond logs, issue sequencing).
- **Structural rule:** every issue past Issue 01 must reference the PRD section that justifies it. No more "Decisions to make in this issue" sections — design-level decisions are pulled up into the PRD, where they're visible across the phase rather than buried per-issue.
- Issue 01 itself has been retrofitted: its original "Decisions to make" block (Terraform vs CDK, state backend, module layout, cert handling) is acknowledged as PRD-level and deferred to the PRD draft pass that's now landed.

## Decisions captured during grilling (will be absorbed into PRD)

Running list of design decisions settled in grilling sessions. The PRD will absorb these with full rationale; this is the working scratch.

### 2026-05-21 grilling session

1. **Two-gate exit criterion** (ship gate + retirement gate). Ship gate = ~25 Macs enrolled, presence accurate within 60s, login+TOTP works. Retirement gate = Mac rows removed from spreadsheet on a team-picked date ~2 weeks after ship gate. No "operator usage" metric.
2. **Presence definition.** `last_seen` in Postgres updated by ingest worker from 30s heartbeats; online threshold = 90s; IoT Core lifecycle events as fast-path for online → offline. Timestream deferred to Phase 2.
3. **Four-wave rollout** (Bench → Pilot site → Mac fleet → Linux tail). Wave 3 (Linux) deferred out of Phase 1 exit gate — install script built in Phase 1, but actual Pi/Radxa enrollment runs as a parallel track. ADR candidate.
4. **Issues-led, PRD-emergent.** Issue 01 keeps running; PRD assembles from grilling sessions; structural rule: post-Issue-01 issues reference PRD sections instead of containing "Decisions to make" blocks.
5. **First-run admin** for auth bootstrap (chosen over manual runbook seed). Forced TOTP enrollment on first login, 10 hashed recovery codes, per-account lockout (5 attempts → 15 min), JWT 1h + refresh 24h (hashed in Postgres, revocable). ADR candidate (security trade-off worth documenting).
6. **Enrollment-first registry model.** A device row exists only after successful enrollment. No spreadsheet seed step, no `expected` status. The spreadsheet retains its "what should exist" job through Phase 1 rollout; the wave structure surfaces enrollment failures via the engineer running each wave checking the dashboard against the spreadsheet.
7. **Bootstrap key: static, bundled in install package (ADR-017).** Supersedes ADR-014. Secrets Manager → CI → install package. Hardening: per-source-IP rate limit + hostname-convention anomaly alert + audit log. Blast radius bounded to registry pollution (per-device mTLS certs minted at enrollment are not granted by the bootstrap key).
8. **Per-device view: static fields + presence + cert-expiry, no live actions.** Cert-expiry surfaced in Phase 1 even though rotation lands in Phase 4 — early-warning signal if Phase 4 slips.
9. **Polling, not WebSocket, in Phase 1.** 10s poll on fleet-list and per-device pages. Structural rule: all live data through TanStack Query (or equivalent) — no raw `setInterval` in components. Preserves cheap WebSocket migration when Phase 2 needs it for command-results.
10. **First-run admin: ADR explicitly declined.** The bootstrap pattern (Q5) was considered for an ADR in this session; reviewer declined — operational discipline (claim admin account immediately on first deploy) is cheap with a small team, security window is brief, and the doc overhead wasn't judged worth it. Recorded here so future readers see the deliberation happened.
11. **Fargate workers (not Lambda) for all MQTT-side ingest (ADR-018).** Picked over Lambda after reconsideration. Drivers: paradigm parsimony with the API service (also Fargate), AI-agent-development friendliness (containers behave identically locally and deployed), persistent Postgres connection pool, sweeper-as-goroutine elegance. Cost ~$8-15/month per worker accepted. Pattern holds across all phases — no Lambda in the ingest path ever.
12. **Site-scoped authorization enforced from day one.** Phase 1 operators (staff) all get `'*'` site allowlist (full access); enforcement machinery is in place from the first endpoint. Schema: `operator_sites` join table (operator_id, site_id, granted_at). Staff rows hold `site_id = '*'` semantic value (or a `is_staff` flag — TBD at implementation). Structural rule: every device-touching handler runs through a `scopedDeviceQuery(ctx, ...)` helper; integration test fails any handler that returns device data without using it (similar posture to ADR-012's idempotency gate). No ADR — architecture.md already establishes the per-site authz requirement; this is execution-level commitment to enforce now rather than retrofit.
13. **Phase 0 dev-mac-mini-emile is decommissioned, not migrated.** Manual cert revoked, thing deleted, agent uninstalled. The same Mac becomes the Wave 0 (Bench) device, re-provisioned fresh through the new install module against Terraform-managed IoT Core resources. Role going forward: bench-only — enrolls/decommissions on demand, not a permanent fleet member. Forces Wave 0 to exercise the install module on a clean machine, which is its purpose. Phase 0 runbook now carries a "do not re-run for fleet devices" banner.

## Issues so far

Vertical-slice breakdown of Phase 1 from the 2026-05-21 `/to-issues` pass against the PRD. Each issue is a tracer bullet — a complete vertical slice that's demoable on its own. Dependency order shown; see each issue's "Blocked by" for specifics.

### Track A — Critical path to Wave 0

- [Issue 01 — Terraform infra: bring Phase 0 IoT Core resources under code](./issues/01-terraform-iot-core.md) (AFK, in flight)
- [Issue 02 — Settle the three Phase 1 TBDs](./issues/02-settle-tbds.md) (HITL — grilling session; unblocks several)
- [Issue 03 — Bench enrollment end-to-end](./issues/03-bench-enrollment.md) (AFK)
- [Issue 04 — Operator login (first-run admin + password + JWT)](./issues/04-operator-login.md) (AFK)
- [Issue 05 — Mandatory TOTP + recovery codes](./issues/05-totp-recovery-codes.md) (AFK)
- [Issue 06 — Site-scoped authorization with `*` allowlist for staff](./issues/06-site-scoped-authz.md) (AFK)
- [Issue 07 — Heartbeat ingest + online derivation](./issues/07-heartbeat-ingest-presence.md) (AFK)
- [Issue 08 — Sweeper + IoT lifecycle fast-path](./issues/08-sweeper-lifecycle-fastpath.md) (AFK)
- [Issue 09 — Cert expiry surfaced on per-device view](./issues/09-cert-expiry-per-device.md) (AFK)
- [Issue 10 — Bootstrap key in Secrets Manager + CI integration + production hardening](./issues/10-bootstrap-key-secrets-manager.md) (AFK)
- [Issue 11 — mac-mini-rollout install module for CP agent](./issues/11-install-module-cp-agent.md) (AFK; sister-repo work)
- [Issue 12 — Wave 0 (Bench) end-to-end smoke](./issues/12-wave-0-bench-smoke.md) (HITL)
- [Issue 13 — Wave 1 (Pilot site) rollout](./issues/13-wave-1-pilot-site.md) (HITL)
- [Issue 14 — Wave 2 (Mac fleet) bulk rollout](./issues/14-wave-2-mac-fleet.md) (HITL — ship-gate milestone)
- [Issue 15 — Spreadsheet retirement (Mac portion)](./issues/15-spreadsheet-retirement.md) (HITL — retirement gate)

### Track B — Dashboard

- [Issue 16 — Dashboard scaffold + auth flow](./issues/16-dashboard-scaffold-auth-flow.md) (AFK)
- [Issue 17 — Fleet view](./issues/17-fleet-view.md) (AFK)
- [Issue 18 — Per-device view](./issues/18-per-device-view.md) (AFK)

### Track C — Operational instrumentation

- [Issue 19 — Structured logs + correlation ID library](./issues/19-structured-logs-correlation-ids.md) (AFK; lands early)
- [Issue 20 — Audit log surface](./issues/20-audit-log-surface.md) (AFK)
- [Issue 21 — Alarms](./issues/21-alarms.md) (AFK)

### Track E — Linux parallel track (does NOT gate Phase 1 exit)

- [Issue 22 — One-page Linux install script](./issues/22-linux-install-script.md) (AFK)
- [Issue 23 — Wave 3 (Linux tail) rollout](./issues/23-wave-3-linux-tail.md) (HITL)
