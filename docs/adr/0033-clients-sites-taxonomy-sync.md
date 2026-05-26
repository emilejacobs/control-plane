# ADR-033: Clients/sites taxonomy — daily mirror sync from upstream HTTP API

**Status:** Accepted (2026-05-26)

**Context.**

CP needs client + site (store) data to surface on the device-page Deployment card, to populate pickers (assign device to site, edit operator allowlists for [#16](https://github.com/emilejacobs/control-plane/issues/16)), and for any future multi-tenant feature that filters by site/client. The canonical data lives in a separate uKnomi-internal Aurora MySQL database (the trip-logic team's "drive-thru store configuration" system, same AWS account, not a data dump — a critical system with its own ownership).

CP today has its own `clients` and `sites` Postgres tables (migrations [008](../../internal/cp/storage/migrations/008_sites.sql) + [009](../../internal/cp/storage/migrations/009_devices_site.sql)) — bootstrapped at the schema level but never populated with real data. Every device today has `site_id = NULL` and shows "Unassigned" in the dashboard.

The earlier in-tree memory `external_mysql_taxonomy` (2026-05-24) proposed direct MySQL access via a Fargate scheduled mirror-sync task. Subsequent conversation with the trip-logic team surfaced that they expose an HTTP API at `api.uknomi.com` covering the same data:

```
POST /user/signin   → returns a Bearer token
GET  /brand         → list of active brands (e.g. "Burger King", "Dunkin Donuts")
GET  /brand/{id}/store → stores + their sites + their client info for that brand
```

Auth is two-layered: AWS IAM role-ARN allowlist at the network layer + Bearer token (from `/user/signin`) at the application layer. Brand is the API's top-level navigation key, but in CP's domain model **Client is at the top** (a uKnomi customer organization, e.g. "Rao", which may operate one or more brands — Rao operates both BK and Dunkin franchises). The hierarchy uKnomi cares about for grouping/filtering is **Client → Site**; Brand is incidental metadata.

This ADR locks the access pattern (sync vs live vs hybrid), the entity model in CP, the soft-delete semantics, the trigger mechanics (scheduled + manual), and the auth approach. The companion implementation issue covers the actual code.

**Decision.**

### 1. Mirror sync into local Postgres (not live read, not hybrid)

A scheduled Fargate task pulls the upstream taxonomy daily into CP's existing `clients` and `sites` tables. The dashboard, pickers, and authz all read from local Postgres. The upstream API is **never** in CP's request path.

Rejected alternatives:
- **Live read against upstream.** Tight coupling — API outage breaks CP's dashboard. CP today has zero per-request dependencies on services it doesn't operate; the live-read path would establish a new failure mode for a separate team's system.
- **Hybrid (cache with live miss).** Same drift window as sync but adds the live-read failure mode on cold cache. No clear win.

### 2. Daily cadence at 00:05 UTC

Scheduled via EventBridge → `ecs:RunTask` on a new `uknomi-cp-taxonomy-sync` task def — verbatim mirror of the audit-mirror wiring per [ADR-023](./0023-fargate-scheduled-tasks-for-batch-jobs.md). Cost is negligible (~365 ECS runs/year, ~$3-5/year). Failure-detection window is ≤24h vs ≤7d for weekly.

### 3. Single Fargate task triggered two ways: scheduled + manual button

The same `cmd/taxonomy-sync` binary runs in both contexts. Manual trigger is a "Force sync now" button on the Settings page that calls `POST /taxonomy/sync` on cp-api, which in turn invokes `ecs:RunTask` for the same task def. This preserves single-code-path parity (no drift risk between scheduled and manual), keeps cp-api's IAM narrow (`ecs:RunTask` + `iam:PassRole` only — no direct upstream-API connectivity), and provides operators an escape hatch when they just added a store upstream and don't want to wait 24h.

Cold-start cost (~30-60s to bring up the ECS task) is acceptable because the manual button is a rare-exception action.

Rejected: inline-in-cp-api execution (would add MySQL/HTTP client to cp-api's dependency surface + risk request-blocking) and a long-running cp-taxonomy-sync service (~$15-30/mo for a workflow that runs once a day).

### 4. CP hierarchy stays Client → Site; Brand is metadata

The upstream model is Brand-centric (a Brand groups its franchisee Clients; each Client owns Sites). CP's operational grouping is and remains **Client → Site**. Brand is captured per Site as flat metadata (`sites.brand_name` + `sites.brand_external_id`) for display and traceability; CP does not gain a `brands` table or model Brand as a hierarchy level.

A Client can operate multiple Brands (per user direction 2026-05-26: "Rao has Burger King and Dunkin Donuts"). The sync iterates active brands via `/brand`, walks each brand's stores via `/brand/{id}/store`, dedupes Clients across brands, and stamps the Brand on each Site row.

### 5. Soft-delete with dual-signal detection

Mirror tables gain `active boolean NOT NULL DEFAULT true` and `last_synced_at timestamptz`. The sync algorithm marks records inactive based on two signals:

- **API flag**: API returns the record with `active=false` → CP marks inactive
- **Absent from sync**: record not present in this sync's responses (brand went inactive, record hard-removed upstream) → CP marks inactive via post-sync sweep (`UPDATE … WHERE last_synced_at < $sync_start_time`)

**Never hard-delete** — `devices.site_id` and `operator_sites.site_id` reference local `sites.id`; cascading deletes would break device assignments and authz grants. Reactivation is free: next sync that observes the record flips `active = true`.

Pickers (assign device, edit allowlist) filter to `active=true` only. Display surfaces (existing device's site name, existing operator's allowlist) render inactive entities with an "Inactive" badge so operators can see staleness rather than silent disappearance.

### 6. Migration 019 — additive schema

```sql
ALTER TABLE clients
    ADD COLUMN external_id     text,
    ADD COLUMN active          boolean NOT NULL DEFAULT true,
    ADD COLUMN last_synced_at  timestamptz;
CREATE UNIQUE INDEX clients_external_id_uq ON clients (external_id);

ALTER TABLE sites
    ADD COLUMN external_id        text,
    ADD COLUMN active             boolean NOT NULL DEFAULT true,
    ADD COLUMN last_synced_at     timestamptz,
    ADD COLUMN brand_name         text,
    ADD COLUMN brand_external_id  text;
CREATE UNIQUE INDEX sites_external_id_uq ON sites (external_id);
```

Local `id` UUIDs stay as PKs; `external_id` is the upsert key. No existing rows to backfill (clients/sites are unpopulated today). `external_id` is nullable at the schema level (Postgres `UNIQUE` permits multiple NULLs) so the constraint applies only to sync-populated rows.

**Operator-facing IDs**: the upstream `external_id` is what operators refer to when talking about specific sites (per user direction). The dashboard should surface `external_id` alongside or in place of CP's internal UUID where operators interact with site identifiers. Captured in the implementation issue as a UX requirement.

### 7. Auth — HTTPS + IAM role ARN + Bearer token + Secrets Manager

- **Network**: HTTPS over public internet via existing NAT egress (same path Fargate tasks use for IoT Core). No PrivateLink endpoint to `api.uknomi.com` — overkill for daily sync traffic.
- **Network-layer auth**: SigV4-signed requests using the `cp-taxonomy-sync` task role's temporary credentials. The API team allowlists the **task role ARN** (not a user ARN — avoids long-lived access keys).
- **Application-layer auth**: Bearer token from `POST /user/signin` (service-account credentials), held in memory for the run. Re-signs on HTTP 401. No cross-run token caching (runs are 24h apart; one extra sign-in per day is cheap).
- **Credential storage**: AWS Secrets Manager (`uknomi-cp/taxonomy-api-creds`, JSON `{"username","password"}`). Task role gets `secretsmanager:GetSecretValue` on the specific ARN.

**Deployment ordering** (load-bearing for the implementation slice):
1. Terraform creates `cp-taxonomy-sync` Fargate task def + task role.
2. Provide role ARN to the API team for allowlist.
3. Then deploy the sync binary.

A `--dry-run` flag on the binary exercises auth + parsing without writing to Postgres — supports safer bench-side validation.

### 8. Manual button — staff-only + Postgres advisory lock + trigger-and-walk-away

- **Authz**: `is_staff = true` required for `POST /taxonomy/sync`. Admin action with non-zero cost (ECS spin-up + upstream API load).
- **Concurrency**: `cmd/taxonomy-sync` acquires a Postgres advisory lock (`pg_try_advisory_lock(0x74786E73796E63)`) at start; releases at end (or on connection drop if the process crashes). Second concurrent invocation exits gracefully without doing work. No queueing.
- **Feedback**: Button POST returns 202 with the new ECS task ARN. Settings page shows "Last successful sync: 4h ago — 3 clients, 87 sites (87 active)" computed on-demand from `MAX(last_synced_at)` + counts via `GET /taxonomy/status`. Operator refreshes manually to see updated state. No background polling, no auto-refresh, no `sync_runs` audit table — CloudWatch Logs from the task carry the full history.

### 9. Observability mirrors audit-mirror

- CloudWatch Logs group `/uknomi/cp-taxonomy-sync` (structured JSON from the binary)
- Log-metric alarm `uknomi-cp-taxonomy-sync-failure` triggers on any `level=ERROR` line in the most recent run
- Both scheduled and manual runs share logs + alarm (same task def)

**Out of scope for this ADR:**

- Direct MySQL access (superseded by the HTTP API).
- A `brands` table or Brand as a hierarchy level in CP.
- Operator-level brand scoping (`operator_brands` table or brand allowlists) — operators stay site-scoped per the existing model.
- Write-back to the upstream API — read-only.
- Long-lived token caching across sync runs.
- A `sync_runs` audit table — CloudWatch Logs sufficient for v1.
- Pre-flight lock check from cp-api before spawning the manual task — the task itself handles contention; pre-flight is a polish opportunity.
- Auto-refresh / polling on the Settings page for sync completion — operator-driven refresh is sufficient for v1.

**Consequences.**

- (+) **CP's availability decoupled from upstream API.** A degraded `api.uknomi.com` produces stale data, not broken dashboard. The dashboard's site-allowlist, device-page Deployment card, and Operators UI all keep working off the local mirror.
- (+) **Pattern parity with audit-mirror.** Same task-def shape, same EventBridge rule pattern, same log-metric alarm wiring (ADR-023). Operationally this is a "fifth scheduled task" rather than a novel infrastructure.
- (+) **Manual button is an escape hatch, not a routine workflow.** Daily cadence keeps it that way; operators use it only when they just added a store and don't want to wait until tomorrow.
- (+) **CP terminology unchanged.** "Client" and "Site" stay; ~50-file rename avoided. Brand metadata adds operator context without a hierarchy change.
- (-) **Up to 24h staleness** for new upstream changes (mitigated by manual button).
- (-) **Two-layer auth coordination** required before first deploy — task role ARN allowlist (network) + service-account creds (application). Two failure modes to debug.
- (-) **No `sync_runs` history table** — recovering "which run failed last Tuesday and why" requires CloudWatch Logs query, not a `SELECT`. Acceptable for v1; can be added later if audit pressure justifies.
- (-) **Brand is captured but not modeled.** If uKnomi later wants brand-grouped reporting or brand-scoped operators, that becomes a separate schema slice. Today's metadata-only choice is YAGNI-defensible but accumulates a small debt against future brand-aware features.

**Verification.**

- ADR-033 indexed in [docs/decisions.md](../decisions.md).
- Memory `external_mysql_taxonomy` updated to point at this ADR — direct-MySQL approach is superseded; the architecture remains "MySQL is the canonical source; CP reads, doesn't duplicate" but the read happens via the HTTP API.
- CONTEXT.md updated: `Client` definition refined for the Brand context; `Site` definition gains brand-metadata note; new `Brand`, `taxonomy-sync`, `external_id`, `last_synced_at`, `Active flag` terms added.
- Implementation issue filed; first slice ships migration 019 + `cmd/taxonomy-sync` + cp-api endpoints + Settings page UI + bench-environment smoke (real sites populated; dashboard pickers list them).
- API team coordination: task role ARN handed off, allowlist confirmed, `uknomi-cp/taxonomy-api-creds` secret created, sample `/brand` + `/brand/{id}/store` responses captured for fixture-based testing.
