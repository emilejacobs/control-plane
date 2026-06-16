# Fleet notifications v1 — offline / stopped-service / health-red → email + MS Teams

A reconciler in `cp-ingest` that watches the fleet's existing unhealthy signals and pushes
**outbound** notifications when a device or service crosses into (and back out of) an alert
state. Delivery config — the **Teams webhook URL** and the **email recipient list** — is
operator-managed in a new **Settings → Notifications** surface. v1 is deliberately narrow:
three signal kinds, two delivery channels, fire **and** resolve, transition-only.

## Design source of truth

This introduces a **new architectural surface — outbound notification egress** from the CP.
It is the first thing in the system that proactively pushes to operators rather than serving a
dashboard read, so it warrants a new ADR (**ADR-039 — outbound fleet notifications**) capturing:
detection-in-`cp-ingest` (not a separate service), the `alert_state` dedupe table as the
transition boundary, the two-channel notifier (SES email + Teams Workflows webhook), config held
in the existing CP-settings store (not infra), and the system-actor scoping that bypasses operator
site-allowlists. Promote the durable decisions there once this PRD's slices are accepted.

Existing surfaces this builds on:
- **`GET /fleet/alerts` (#21)** — the read-side roll-up of red/yellow probes + stopped services
  (`internal/cp/api/handlers/fleet/alerts.go`, `registry.FleetAlerts`). The detection logic is the
  same shape, but the handler path is **site-scoped and fails closed** with no operator scope, so
  the reconciler cannot call it directly (see Implementation Decisions).
- **`PresenceSweeper` (ADR-018)** — the goroutine-on-a-ticker pattern in `internal/cp/ingest/sweeper.go`
  the reconciler mirrors exactly.
- **`devices.is_online`** — the stored presence flag (the offline signal; not part of `FleetAlerts`).
- **CP-settings store + Settings UI (#84)** — `registry.SetCPSetting` / `GetCPSetting`, the staff-only
  `/settings/*` handlers (`internal/cp/api/handlers/settings`, e.g. the Plate Recognizer token), and
  the dashboard Settings page composed of cards (`web/app/settings/page.tsx`,
  `PRTokenSettingsCard.tsx`, `web/lib/api/settings.ts`). The notification config reuses this whole
  pattern — secret values write-only over the API, a new card on the same page.

## Problem Statement

When a device drops offline, a store service stops, or a health probe goes red, the operator only
finds out by **looking at the dashboard**. There is no push. A Mac that dies overnight, a Colima
container that crashes, or a site that loses internet is invisible until someone happens to open the
Overview — which, for a ~63-device fleet across many client sites, can be hours. The operator wants
to be told, in the channels they already watch (email and the team's MS Teams), the moment something
goes wrong and the moment it recovers — and to manage **who gets emailed** and **which Teams channel**
from the dashboard, without editing infrastructure or redeploying.

## Solution

A background **reconciler** inside the existing `cp-ingest` process ticks on an interval, takes a
fleet-wide snapshot of the three unhealthy signals, and diffs that snapshot against an `alert_state`
table that records which alerts are currently open. Transitions — and only transitions — produce
notifications:

- A signal that is **newly unhealthy** (not already an open row) opens an alert and is queued to fire.
- A signal that **was open and is now healthy** closes the alert and is queued as a recovery.
- A signal that is **still unhealthy** (already open) produces nothing — the dedupe table makes the
  reconciler idempotent, so no repeat spam every tick.

All transitions found in a single tick are **coalesced into one digest per channel** — a site that
loses internet (many devices offline at once) produces a single grouped email and a single Teams card
listing every newly-affected device, not dozens of messages. Each digest is delivered to whatever the
operator has configured in **Settings → Notifications**:

- **Email**, sent directly via **Amazon SES** to the recipient list typed into Settings. Adding or
  removing a recipient is just editing the list — no subscription/confirmation flow.
- **MS Teams**, by POSTing to the **Workflows incoming-webhook** URL held in Settings (seeded with
  the already-provisioned default, replaceable by staff).

Each channel can be independently empty (no recipients / no webhook) — that channel is simply skipped.
The reconciler reads this config from the CP-settings store **each tick**, so edits take effect without
a redeploy.

## User Stories

1. As an operator, I want an email the moment a device goes offline, so that I learn about an overnight failure without watching the dashboard.
2. As an operator, I want the same alert in MS Teams, so that the whole on-call team sees it in the channel they already watch.
3. As an operator, I want a notification when a store service (e.g. the ALPR container, the Edge UI) stops, so that I can act before the site notices missing data.
4. As an operator, I want a notification when a health probe goes red, so that I catch a degraded device before it fails outright.
5. As an operator, I want a **recovery** notice when a device comes back online, so that I know the issue resolved without re-checking the dashboard.
6. As an operator, I want a recovery notice when a stopped service starts again and when a red probe goes green, so that every alert closes the loop.
7. As an operator, I want to be told **once** per incident, not every tick it stays broken, so that my inbox and the Teams channel aren't buried in repeats.
8. As an operator, I want a site-wide internet outage to arrive as **one grouped message**, not one-per-device, so that a 20-device drop doesn't flood me with 20 emails.
9. As an operator, I want each notification to name the affected device(s) and the signal that tripped, so that I can triage without opening the CP first.
10. As an operator, I want the message to identify the device by the name/site I recognize (not just a UUID), so that I know where to go.
11. As staff, I want a **Settings → Notifications** section in the dashboard, so that I manage notification config in the same place as the other account-wide settings.
12. As staff, I want to enter and edit the **list of email recipients** in Settings, so that I can change who gets alerted without an engineer or a deploy.
13. As staff, I want to view and replace the **Teams webhook URL** in Settings, so that I can point alerts at a different channel or rotate a leaked URL myself.
14. As staff, I want the Teams webhook to come **pre-configured** with the URL we already set up, so that Teams alerts work out of the box without me pasting anything.
15. As staff, I want the webhook URL treated as a secret — write-only over the API, kept out of logs and audit payloads — so that the signed URL can't leak from a GET response or a log line.
16. As staff, I want the Settings page to show whether each channel is configured (recipient count, webhook set/not-set), so that I can confirm notifications will actually go somewhere.
17. As staff, I want an enable/disable switch for notifications, so that I can pause all alerts during planned maintenance without losing the config.
18. As an operator, I want a config change to take effect within a tick, so that I don't have to wait for a deploy after editing recipients.
19. As an operator, I want the reconciler to keep working across a `cp-ingest` restart, so that a deploy or crash doesn't re-fire every open alert or lose one that opened during the restart.
20. As an operator, I want the reconciler to cover the whole fleet regardless of per-operator site allowlists, so that an alert isn't suppressed just because no human is scoped to that site.
21. As an operator, I want delivery failures (SES or Teams down) logged and retried on the next tick, and the alert left un-notified until it succeeds, so that a transient outage doesn't silently drop an alert.
22. As an operator, I want notification volume bounded even in a worst case, so that the system can't turn a fleet-wide event into a self-inflicted message storm.
23. As an engineer, I want the reconciler to live in the existing `cp-ingest` process, so that no new service, deploy target, or runbook is introduced for v1.
24. As an engineer, I want detection to reuse the existing unhealthy-signal definitions, so that what triggers a notification stays consistent with what the dashboard shows.
25. As an engineer, I want the notifier behind a small interface with a fake, so that the diff/transition logic is testable without AWS or a live Teams endpoint.
26. As a security owner, I want the reconciler to run as a system actor with least-privilege rights (`ses:SendEmail` + read the settings), so that the notification path can't read or mutate anything else.

## Implementation Decisions

### Notification config lives in the CP-settings store (not infra)
Reuse the #84 CP-singleton settings pattern (`registry.SetCPSetting`/`GetCPSetting`, new keys). Three
settings:
- **`notifications.teams_webhook_url`** — the Teams Workflows webhook. A **secret**, handled exactly
  like the PR token: `PUT` sets it, `GET` reports only whether it is set (plus a non-sensitive masked
  preview, e.g. host only); never returned in full, never logged, never in an audit payload. **Seeded
  by a DB migration** with the already-provisioned default URL so Teams works on first deploy.
- **`notifications.email_recipients`** — the recipient list (a small JSON array / newline list of
  addresses). **Not secret**: `GET` returns the actual list so the UI can render and edit it; `PUT`
  replaces it. Validated as well-formed addresses on write.
- **`notifications.enabled`** — a master on/off so staff can pause alerts without clearing config.

New staff-only handlers under `internal/cp/api/handlers/settings` (`notifications.go`) following the
`pr_token.go` shape. The reconciler reads these from the registry each tick (cheap singleton reads);
an empty channel is skipped, and `enabled=false` short-circuits the whole tick's delivery (detection
+ `alert_state` bookkeeping still run so state stays accurate while paused — only the send is gated).

### Detection — a system-scoped fleet read, not the `/fleet/alerts` handler path
`registry.FleetAlerts` resolves an operator `SiteFilter` from the request context via
`authz.ScopeFromContext` and **fails closed** (empty roll-up) when none is present. A background
goroutine has no request and no operator scope, so it must not reuse that path. Add a **system-actor
fleet-unhealthy read** on the registry that returns the fleet-wide snapshot with no site filter
applied. It covers three signal kinds in one snapshot:
- **offline** — `devices` where `is_online = false` (the presence flag; *not* in `FleetAlerts`).
- **service-stopped** — `device_services` where `state = 'stopped'` (subject = service name).
- **probe-red** — `device_health_probes` where `status = 'red'` (subject = probe name). **Red only**
  for v1; yellow is dashboard-only and excluded to keep notification volume meaningful.

The snapshot is a flat list of alert identities, each `(kind, device_id, subject)` where `subject`
is the service/probe name (empty for offline). The device's human label/site is joined in for
rendering.

### `alert_state` table — the transition boundary and dedupe key
A new table keyed by alert identity `(kind, device_id, subject)`, with `opened_at`, `last_notified_at`,
`notify_attempts`, and `resolved_at` (NULL while open). It is the **source of truth for what has
already been notified**, which is what survives a `cp-ingest` restart (the in-memory presence model
does not). Registry methods: load currently-open rows, open a new alert, mark resolved, and record a
successful notification. An alert is "open" iff a row exists with `resolved_at IS NULL`.

### Reconcile diff — fire + resolve, transition-only
Each tick: snapshot ∖ open-rows → **newly opened** (queue to fire); open-rows ∖ snapshot →
**newly resolved** (queue as recovery, set `resolved_at`); intersection → no-op. This makes the loop
idempotent — steady-state unhealthy produces zero messages. A resolved row is retained (not deleted)
so history exists and a flapping signal re-opens a fresh row rather than mutating a closed one.

### Per-tick digest — one grouped message per channel
All opened + resolved transitions in a single tick are rendered into **one digest** (opened section +
recovered section) and sent **once per channel**. A site-wide outage → one email + one Teams card.
A configurable per-tick cap bounds the worst case; anything beyond the cap is summarized as a count
(and logged) rather than enumerated, so the system can never amplify a fleet event into a flood.

### Notifier — a small interface with two implementations + a fan-out
`Notifier.Notify(ctx, digest) error`. Implementations:
- **SES notifier** — sends the rendered digest directly to the configured recipient list via Amazon
  SES (`SendEmail`). Subject + body rendered from the digest; a single verified sender identity is the
  `From`. Empty recipient list → no-op.
- **Teams notifier** — POSTs an adaptive-card / message JSON to the configured Workflows webhook URL.
  Empty URL → no-op. A non-2xx response is surfaced as an error so the reconciler retries.
- **Fan-out notifier** — calls both; a failure in one channel does not suppress the other, and any
  channel failure leaves the alert **not** marked notified so the next tick retries (at-least-once,
  accepting possible duplicate delivery on retry — preferred over silent loss).

Both impls take their per-send config (recipients / webhook URL) as call arguments sourced from the
settings read, so the notifiers themselves hold no config state and stay trivially testable.

### Reconciler goroutine — mirrors `PresenceSweeper`
A `NotificationReconciler` with `Run(ctx)` ticking on an interval (default in the minutes range — far
slower than the 30s presence sweep; notifications don't need second-granularity), a `now func()` seam,
config struct with defaulted fields, structured-log audit lines (`audit.notify` open/resolve/deliver),
and a `notify.tick` heartbeat each tick. Wired in `cmd/cp-ingest/main.go` as one more
`go func(){ ... .Run(ctx) }()` alongside the existing sweepers. Always started; it self-gates on the
settings (`enabled` + non-empty channels), so there is no deploy-time flag.

### Settings UI — a card on the existing Settings page
A `NotificationSettingsCard` on `web/app/settings/page.tsx` alongside `PRTokenSettingsCard`, with
client calls added to `web/lib/api/settings.ts`. It shows: the enable/disable switch; an editable
email-recipient list (with the current addresses); and the Teams webhook as a masked/"configured"
indicator with a replace action (write-only, mirroring the PR-token card UX). Follows the existing
card styling (`.page > .card` auto-spacing — no hand-added margins).

### Infrastructure — `terraform-deploy`, manual apply
Drops the previously-planned SNS topic and the Secrets Manager secret (config now lives in the
CP-settings store). Remaining infra in `infra/terraform-deploy` (shared, manual apply per ADR-027):
the `cp-ingest` task-role gains **`ses:SendEmail`** (scoped to the verified identity ARN). The
**operator prerequisite** is verifying one SES sender identity (domain or from-address) and moving
the SES account out of the sandbox into production sending.

## Testing Decisions

A good test here asserts **external behavior at module boundaries** — given a snapshot and a set of
open rows, what gets queued and what gets persisted — never the internal shape of the diff.

- **Reconcile diff logic** (the core): table-driven unit tests against a **fake store + fake notifier**.
  Cases: new signal fires exactly once; same signal next tick is silent; resolved signal queues a
  recovery and sets `resolved_at`; a delivery failure leaves the row un-notified so the next tick
  retries; multiple transitions in one tick coalesce into a single digest; the per-tick cap summarizes
  the overflow; `enabled=false` runs detection/state but sends nothing. Prior art:
  `internal/cp/ingest/sweeper_test.go`, `device_services_sweeper_test.go`.
- **Notifier implementations**: SES notifier against a fake/stubbed SES client asserting the send call
  shape + that an empty recipient list is a no-op; Teams notifier against an `httptest` server
  asserting the POST body, the empty-URL no-op, and that a non-2xx surfaces as an error (so the
  reconciler retries). Fan-out: one channel failing still calls the other and returns an error.
- **`alert_state` registry methods**: integration tests against testcontainers Postgres (Docker-gated,
  run in CI — not locally per the repo's standing constraint). Open → load-open returns it; resolve →
  load-open excludes it; re-open after resolve creates a fresh row. Prior art: the existing registry
  integration tests.
- **Settings handlers**: unit tests on the new `notifications.go` handlers — webhook is write-only
  (GET never returns the value), recipient list round-trips, invalid addresses rejected on PUT,
  staff-gate enforced. Prior art: `PRTokenSettingsCard.test.tsx` / the existing settings handler tests.
- **Settings card (frontend)**: a `NotificationSettingsCard.test.tsx` mirroring
  `PRTokenSettingsCard.test.tsx` — renders current state, edits recipients, replaces the webhook,
  toggles enabled.

The user wants tests on: the **reconcile diff logic**, the **two notifier implementations + fan-out**,
the **`alert_state` registry methods**, and the **settings handlers + card** (config is now a
first-class part of the feature). Wiring in `main.go` and the Terraform are not unit-tested.

## Out of Scope

- **Yellow probes** — dashboard-only; only red trips a notification in v1.
- **Per-recipient / per-site routing** — v1 fans every alert to the single recipient list + single
  Teams webhook; no "site X's alerts go to person Y."
- **Severity, escalation, ack/snooze, on-call schedules, paging (PagerDuty/Opsgenie), SMS.**
- **Per-channel message customization / templates beyond the built-in digest layout.**
- **Per-recipient throttling / quiet hours / digest scheduling** beyond the per-tick coalescing.
- **New signal kinds** beyond offline + stopped-service + red-probe (e.g. cert-expiry, snapshot-lag,
  disk) — additive later behind the same reconciler.
- **Backfill of already-open conditions at first deploy** — see Further Notes.
- **A notification history / sent-log view in the UI** — `alert_state` retains rows but v1 surfaces no
  page for them.
- **The operator prerequisite** (verifying an SES sender identity + exiting the SES sandbox) — it is
  documented, not automated.

## Further Notes

- **First-deploy behavior:** on the first tick after the feature ships, every currently-unhealthy
  signal is "newly opened" (the table starts empty) and will fire in one digest. This is acceptable
  (it's an accurate snapshot of current state) but worth calling out so it isn't mistaken for a storm
  bug. The per-tick cap protects the worst case; staff can also leave `enabled=false` until ready.
- **Default Teams webhook** is seeded by migration so Teams works immediately; staff can replace it in
  Settings. It is a signed bearer URL — hence the secret/write-only handling.
- **SES sandbox:** until the account is moved to production, SES only delivers to verified addresses;
  this is the one operator prereq and should be done before relying on email.
- **At-least-once, not exactly-once:** a delivery that succeeds at the channel but fails before the row
  is marked notified will re-fire next tick. Chosen deliberately over silent loss.
- **Config read each tick** keeps "edit recipients, no deploy" true; the reads are cheap CP-singleton
  lookups and can gain a short in-process cache if ever needed.
- **Why in `cp-ingest`:** it already owns the sweepers, the registry, and runs continuously. A separate
  notifier service would duplicate all of that for v1's narrow scope.
- **ADR-039** should be written when these slices land, formalizing the outbound-egress surface.
- **Slicing:** natural tracer-bullet order is (1) `alert_state` table + registry methods + system
  fleet-unhealthy read; (2) notification settings (store keys + seed migration + handlers + Settings
  card); (3) reconcile diff with a fake notifier — proves transition logic end-to-end; (4) SES notifier
  + Teams notifier + fan-out, reading config from settings; (5) `main.go` wiring + Terraform + the ADR.
  Run `to-issues` on this PRD to cut them.
