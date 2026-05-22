# Issue 06 — Site-scoped authorization with `*` allowlist for staff

Status: ready-for-agent
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

- [ ] Schema migrations create `clients`, `sites`, `operator_sites` and a sentinel/flag representation for staff full access.
- [ ] The first-run admin from #04 has full access to all sites by default after this slice deploys.
- [ ] A unit test creates a staff operator and a non-staff operator with one site grant, runs `scopedDeviceQuery`, and asserts the row counts match (staff sees all; non-staff sees only their site).
- [ ] CI gate test fails on a deliberately-broken handler that bypasses `scopedDeviceQuery`, passes on the real handlers.
- [ ] `GET /devices` and `GET /devices/{id}` continue to return all devices for staff (no behavioral change for Phase 1's operator set).
- [ ] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 04 (operators table).
