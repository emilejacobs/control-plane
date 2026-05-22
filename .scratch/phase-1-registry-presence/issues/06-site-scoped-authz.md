# Issue 06 — Site-scoped authorization with `*` allowlist for staff

Status: done
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 23–25, § Implementation Decisions (AuthZ module).
- Architecture: `docs/architecture.md` § Security — "Per-site authorization on operator JWTs (site allowlist claim, enforced server-side on every endpoint)."

## What to build

The `AuthZ` deep module — the `scopedDeviceQuery` helper, the middleware that injects the operator's site allowlist into request context, the `clients` / `sites` / `operator_sites` tables, and the CI gate test that fails any device-touching handler that bypasses the helper. All Phase 1 operators get a `'*'` allowlist (or an `is_staff` flag — settle representation at implementation time) granting full fleet access. The machinery exists so local field-operator accounts can land in a later phase without retrofit.

Scope:

- Schema: `clients` (id, name), `sites` (id, client_id, name), `operator_sites` (operator_id, site_id, granted_at) — sentinel for staff `*` allowlist to be settled. Migrations.
- A small seed mechanism: the first-run admin (from #04) is automatically granted staff full access.
- `AuthZ` module: `Scope(ctx) → SiteFilter` (returns either `all` or `[]siteID`), `scopedDeviceQuery(ctx, baseQuery)` composes the filter into any device-touching query.
- Middleware: resolves the operator's allowlist from `operator_sites` (cached per-request) and injects it into request context.
- Apply the helper to `GET /devices` and `GET /devices/{id}` from #03. Staff continue to see everything; later non-staff operators are filtered.
- **CI gate test** (similar posture to ADR-012's idempotency gate): scans handler code; any device-returning handler that doesn't reach the helper fails CI. Pattern: static analysis or a runtime hook in tests that records which queries ran during which handler call and asserts they all went through the helper.

## Acceptance criteria

- [x] Schema migrations create `clients`, `sites`, `operator_sites` and a sentinel/flag representation for staff full access.
- [x] The first-run admin from #04 has full access to all sites by default after this slice deploys.
- [x] A unit test creates a staff operator and a non-staff operator with one site grant, runs `scopedDeviceQuery`, and asserts the row counts match (staff sees all; non-staff sees only their site).
- [x] CI gate test fails on a deliberately-broken handler that bypasses `scopedDeviceQuery`, passes on the real handlers.
- [x] `GET /devices` and `GET /devices/{id}` continue to return all devices for staff (no behavioral change for Phase 1's operator set).
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 04 (operators table).

## Comments

### 2026-05-21 — landed in 10 cycles (`6459d7a`..`9c30faa`)

The `authz` deep module — site-scoped authorization enforced from the
first endpoint.

- Cycle 1: `authz` package — `SiteFilter`, `ScopeForOperator` (staff → All).
- Cycle 2: migration `008` (`clients`, `sites`, `operator_sites`);
  non-staff scope resolved from `operator_sites`.
- Cycle 3: migration `009` (`devices.site_id`); `ScopedDeviceQuery`.
- Cycle 4: non-staff scoped query sees only granted sites (AC3).
- Cycle 5: the `Scope` middleware injects the `SiteFilter` into context.
- Cycle 6: `GET /devices/{id}` site-scoped — `registry.GetByID` reads
  the scope, fails closed without one.
- Cycle 7: `GET /devices` — the site-scoped fleet list (new endpoint).
- Cycle 8: first-run admin has full-fleet access (AC2).
- Cycle 9: the CI gate — `ScopedMarker` + a pgx query tracer (AC4).
- Cycle 10: docs.

**Staff representation settled.** The issue left the staff `*` allowlist
"TBD — sentinel vs flag." Settled on the **`is_staff` flag**, which
`operators` already carries (migration `003`) and the JWT `TokenClaims`
already expose. No `'*'` sentinel row in `operator_sites`; staff need no
rows there at all. The "small seed mechanism" from the scope is therefore
moot — `ClaimFirstRunAdmin` already sets `is_staff = true`, which *is*
the full-access grant.

**Premise correction.** The issue's schema bullet listed only `clients`
/ `sites` / `operator_sites`, but `ScopedDeviceQuery` cannot filter
devices without a `devices.site_id` (the PRD § Data model has it).
Migration `009` adds it — nullable; Phase 1 enrollment does not yet
capture a site and every Phase 1 operator is staff, so a null `site_id`
is harmless until non-staff operators arrive.

**`GET /devices` built here.** The list endpoint did not exist (only
`GET /devices/{id}`). It is built in this slice — a list is where
over-serving leaks most — and #17 (fleet view) will shape its response.

**CI gate.** Runtime query tracer (chosen over a static source scan): a
pgx `QueryTracer` records every SQL statement; `ScopedDeviceQuery` stamps
a `/* authz:scoped */` marker; `authz.UnscopedDeviceReads` flags any
`devices` read lacking it. The gate test asserts the real handlers pass
and a raw query is caught.

**Documentation criterion.** Discharged — `architecture.md` (authz
module row, Security section, Storage, module credits) and `CONTEXT.md`
("Client", "Site allowlist") updated in cycle 10. **No ADR** — PRD
decision 12 explicitly declines one for site-scoped authz
("architecture.md already establishes the per-site authz requirement;
this is execution-level commitment").
