# ADR-039: Outbound fleet notifications — cp-ingest reconciler, alert_state dedupe, SES + Teams

**Status:** Accepted (2026-06-16)

**Context.**

The CP has, until now, been **read-only toward operators**: every unhealthy signal (device offline, service stopped, health probe red) is visible only by opening the Overview dashboard. For a ~63-device fleet across many client sites, a device that dies overnight or a Colima/ALPR container that crashes can stay invisible for hours. Operators want to be *pushed* — in the channels they already watch (email + the team's MS Teams) — the moment something breaks and the moment it recovers, and to manage *who* gets emailed and *which* Teams channel from the dashboard, without editing infrastructure or redeploying. This is the first surface that originates an *outbound* message rather than serving a dashboard read, so the egress shape, the dedupe boundary, and the config home are decisions worth recording. PRD: `.scratch/fleet-notifications/PRD.md`; issues #94 (PRD) and #95–#99.

**Decision.**

1. **Detection lives in `cp-ingest`, as a reconciler goroutine** — not a new service. It already owns the sweepers, the registry, and runs continuously; a separate notifier service would duplicate all of that for v1's narrow scope. The `NotificationReconciler` mirrors `PresenceSweeper` (ADR-018): a ticker that each tick diffs the live fleet-unhealthy snapshot against the open alert rows and fires **transition-only** notifications.

2. **`alert_state` is the transition boundary and dedupe key.** A table keyed by alert identity `(kind, device_id, subject)` with a partial unique index enforcing **one open row per identity** (`resolved_at IS NULL`). It is the source of truth for *what has already been notified*, which is what survives a `cp-ingest` restart (the in-memory presence model does not). A still-unhealthy signal is already open + notified → no repeat. Resolved rows are retained as history; a flapping signal re-opens a fresh row.

3. **Detection is a system-actor read that bypasses operator site-scoping.** `registry.FleetAlerts` (#21) resolves an operator `SiteFilter` and **fails closed** to an empty roll-up when none is present — correct for a request, wrong for a request-less goroutine. A dedicated `FleetUnhealthy` read returns the whole fleet's offline devices + stopped services + **red** probes (yellow is dashboard-only) with no site filter applied, so an alert is never suppressed because no human is scoped to that site.

4. **Two channels: SES email + an MS Teams Workflows webhook, behind a fan-out.** Email sends directly via Amazon SES v2 to the configured recipient list (no SNS topic / subscription-confirmation dance — adding a recipient is editing a list); Teams is a plain HTTPS POST to a Workflows incoming-webhook URL. An empty channel is skipped; a failure in one does not suppress the other. Delivery is **at-least-once**: a send that fails leaves the alert un-notified so the next tick retries, preferred over silent loss.

5. **v1 behavior: fire + resolve, per-tick digest.** Both an open and a recovery are notified (closed loop via `resolved_at`); a recovery is sent only for an alert that was actually notified (no recovery for an alert nobody heard about). All transitions in one tick are coalesced into **one digest per channel** — a site-wide outage is one email + one Teams card — with a per-tick cap that summarizes the overflow as a count rather than enumerating it.

6. **Config lives in the `cp_settings` store, not infrastructure.** The enable switch, recipient list, and Teams webhook URL are managed in a staff-only **Settings → Notifications** surface (the #84 CP-settings pattern), read by the reconciler **each tick** so an operator's edit applies with no redeploy. The webhook URL is a signed bearer credential, handled write-only like the PR token — and **not seeded via a git-committed migration** (that would leak it into history); staff paste the provisioned URL once via the card. `enabled=false` keeps `alert_state` accurate but sends nothing (only the send is gated; alerts opened while paused fire on re-enable).

7. **SES is the only infra dependency.** The `cp-ingest` task role gains `ses:SendEmail` (manual apply, ADR-027); the email channel is wired only when `NOTIFICATIONS_FROM_ADDRESS` (the verified sender identity) is set. There is no SNS topic and no Secrets Manager secret for this feature.

**Consequences.**

- (+) Operators learn about failures + recoveries in email + Teams without watching the dashboard; recipients + channel are self-service from Settings.
- (+) No new service, deploy target, or runbook; reuses cp-ingest, the registry, and the CP-settings store.
- (+) Dedupe + restart-durability come from one small table; the diff logic is unit-tested with a fake store + fake notifier.
- (−) **Operator prerequisite:** an SES sender identity must be verified and the SES account moved out of the sandbox before email delivers. Until then only Teams works.
- (−) `enabled=true` with no channel configured marks alerts notified without sending (an operator misconfiguration; the default is `enabled=false`).
- (−) On first enable, every currently-unhealthy signal fires in one digest (accurate, bounded by the per-tick cap, but worth knowing).
- (−) v1 fans every alert to one recipient list + one webhook — no per-site routing, severity, escalation, ack/snooze, or paging.

**Verification.** Reconcile diff (fire-once / silent-repeat / recover+resolve / retry-on-failure / coalesce / cap / disabled-gate), the SES + Teams + fan-out senders, the SettingsConfigSource, and the Settings card are unit-tested (#95–#98). End-to-end (a real device offline → one email + one Teams message, recovery → closing notice) is checked once the SES identity is verified — `N/A until the operator SES prereq is met`.
