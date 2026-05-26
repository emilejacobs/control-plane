# ADR-031: Webhook endpoint registry — CP-wide config primitive

**Status:** Accepted (2026-05-25)

**Context.**

[ADR-030](./0030-edge-ui-per-feature-surface.md) decided that Plate Recognizer config moves from per-device editing in Edge UI to per-device editing in CP, pushed down via a new `pr.config.update` cmd. That decision surfaced a secondary question: where do the *webhook URLs* in PR config come from?

Operators run a small fixed set of webhook endpoints — typically `prod`, `pre-prod`, `dev` tiers — and enable some subset of them per device. Today these URLs are typed into Edge UI per device and stored in `webhooks-meta.json` on disk. Two operational pains follow:

1. **Endpoint URL changes require visiting every device.** When the prod webhook moves to a new URL, every device that has it enabled needs its config re-edited. With 22 stores today and growth ahead, this gets brittle.
2. **No way to answer "which devices use endpoint X."** The config lives only on each device; CP has no fleet-wide view, so there's no programmatic answer to that question.

Until this ADR, every config in CP has been either device-scoped (`devices.service_allow_list_override`) or site-scoped (`sites.client_id`). There is no concept of *fleet-wide named config that devices reference by name and that CP resolves at push time*. The webhook endpoints are the forcing case for introducing one.

The forcing question: **introduce a generic fleet-wide-config primitive now (with webhook endpoints as its first instance), or keep webhook URLs per-device and accept the operational cost?**

Alternatives considered:

1. **Status quo per-device URLs.** Cheapest to implement; pushes the operational pain to the operator at every endpoint rotation. The pain compounds with fleet growth — wrong direction.
2. **Fleet-wide CP variable with no indirection.** A single `prod_webhook_url` value in CP, used uniformly. Doesn't model the real operator workflow (mixed dev/pre-prod/prod across the fleet during testing) and locks the fleet to one tier.
3. **Endpoint registry with per-device references.** CP holds the canonical list of named endpoints; per-device PR config stores *which named endpoints are enabled*; CP resolves names → URLs at `pr.config.update` push time.

**Decision.**

Introduce a CP-wide **webhook endpoint registry** as the first instance of a more general pattern: **named fleet-wide config + per-device references + push-time resolution + explicit fan-out on change**.

### Concretely for webhooks

- **New table `webhook_endpoints`** (`id`, `name`, `url`, `environment`, `created_at`, `updated_at`). `name` is unique within the CP instance (e.g., `transcriber-prod`).
- **Per-device PR config** stores an `enabled_webhooks: string[]` of registry entry names — not URLs.
- **At `pr.config.update` push time**, CP joins the per-device list against the registry, builds the resolved `[{name, url, enabled}]` array, includes it in the cmd payload. Agent receives URLs as today; the registry is invisible on the device.
- **CP UI**: a new top-level "Settings" area (admin-gated) with a "Webhook endpoints" card. CRUD on entries.
- **Fan-out on URL change**: when an operator edits an endpoint URL, CP enumerates devices with that endpoint enabled and offers a one-click "Push update to N devices?" prompt. Each individual push goes through the normal `pr.config.update` cmd flow — same retry/ACK/audit semantics — so partial failures behave like any other multi-device write.
- **Rename semantics**: renaming an endpoint cascades the rename to every per-device reference in the same transaction. Deletion is gated on zero references (operator must disable on all devices first; UI surfaces the offenders).
- **Audit row**: every registry change is audit-logged with operator identity, before/after values, and the list of affected device IDs.

### As a pattern for future fleet-wide config

This ADR explicitly establishes a pattern. Future CP-wide named configs (default service allow-list, default log-allow-list, fleet-wide cert TTL, default reporting intervals) should follow the same shape:

1. CP table holding the named entries
2. Per-device storage references the entries by `name`, not by inlined value
3. Resolution happens at push time — agents never see the registry
4. Fan-out on registry change is an explicit operator confirmation step with audit
5. Rename cascades; delete is gated on zero references

Concrete future use is not committed by this ADR — but when a future feature wants "set this once, applies to many devices," the answer is "use the registry pattern," not "reinvent it."

**Alternatives rejected** are above.

**Consequences.**

- (+) Endpoint URL rotation collapses from "edit N devices" to "edit one row + click push." Operational pain drops sharply.
- (+) Fleet-wide queries are trivially answerable. "Which devices send to `transcriber-prod`?" is a `JOIN` away.
- (+) Establishes the fleet-wide-config pattern. Future CP-wide configs reuse the shape; less ad-hoc reinvention as features land.
- (+) Audit trail of "who changed this endpoint, when, what propagated where" is built-in.
- (+) Agent stays decoupled from the registry concept — agent only ever receives resolved URLs. No code change on the agent side beyond what `pr.config.update` already requires.
- (-) Adds a new top-nav area to CP (Settings). Information-architecture decision worth doing carefully; "Settings" should not become a junk drawer.
- (-) Fan-out partial failures need handled: if 23 devices need update but 5 are offline, CP UI must show which ones are still pending. Solvable via the existing per-device `service_config_last_applied_at` pattern (extended to PR config); not free.
- (-) Renaming an endpoint is a multi-row write. Edge case: if a rename happens mid-push (a fan-out is in-flight when the rename lands), the inflight pushes carry the old name. Mitigation: gate rename on "no inflight fan-outs for this endpoint" (or queue the rename behind them); slice PRD detail.
- (-) The temptation to use the registry pattern for things that *aren't* genuinely fleet-wide is real. Per-device service allow-list overrides exist *because* devices differ. The pattern is for cases where the operator genuinely wants fleet-uniform config with per-device opt-in/out — not for capturing per-device differences.

**Verification.**

- **Resolve-at-push-time semantics**: integration test inserts a webhook endpoint, enables it on a test device, asserts the `pr.config.update` cmd published to that device contains the resolved URL (not the name).
- **Fan-out gating**: dashboard test asserts the "Push update to N devices" prompt enumerates the correct device set when an endpoint URL changes.
- **Rename cascade**: integration test renames an endpoint, asserts all per-device references update atomically in the same transaction.
- **Delete gate**: API test asserts an attempt to delete an endpoint with active references returns an error listing the referring devices, not a successful delete.
- **Audit row**: every registry change writes an audit row including operator ID + before/after + affected device IDs; verified by the standard audit-log test pattern.
- **Pattern stickiness**: when future fleet-wide configs land (the first one will indicate whether the pattern actually generalises), the implementing ADR references this one and either reuses the shape or explicitly justifies deviation.
