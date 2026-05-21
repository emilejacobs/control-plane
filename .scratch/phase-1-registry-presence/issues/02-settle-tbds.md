# Issue 02 — Settle the three Phase 1 TBDs

Status: done
Type: HITL (grilling session)
Completed: 2026-05-21 — grilling session settled all three TBDs into ADR-019 (goose migrations), ADR-020 (CI/CD shape), ADR-021 (all-CloudWatch observability).

## Parent

- PRD: [`PRD.md`](../PRD.md) § Explicit TBDs and § Further Notes — Branches still to grill.
- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1.

## What to build

A grilling session (`/grill-with-docs`) that settles the three Phase 1 TBDs explicitly flagged in the PRD, recording each decision in the canonical place (ADR if it meets the three criteria; PRD update otherwise).

The three TBDs:

1. **Schema migrations tooling.** Candidates: goose, sqlc + sql migrations, embedded `pgx`-driven SQL, alternatives. Decide one for Phase 1, document in PRD. Affects every DB-touching slice from #03 onward.
2. **CI/CD pipeline shape.** Branch model (trunk-based? GitHub Flow?), deployment promotion (single prod vs prod + staging), artifact strategy (one container per service?), Terraform plan/apply flow (manual gate? auto?), where secrets are fetched (Secrets Manager at build time, per ADR-017). Affects #10 (bootstrap key + CI) and #12 (Wave 0 deploy).
3. **Observability platform.** CloudWatch Alarms (default) vs. external (Grafana Cloud, Datadog, etc.). Affects #21 (alarms) and the metrics dashboard story for Phase 2.

The grilling session uses the project's domain glossary and respects existing ADRs. Outcomes:

- For each decision, either an ADR (if the three criteria are met: hard to reverse, surprising without context, real trade-off) or a PRD update.
- PRD's "Explicit TBDs" section updated to reflect resolution.
- Dependent slices unblocked.

## Acceptance criteria

- [ ] Schema migrations tooling decided, documented in PRD (or ADR if warranted).
- [ ] CI/CD pipeline shape decided, documented in PRD (or ADR if warranted).
- [ ] Observability platform decided, documented in PRD (or ADR if warranted).
- [ ] PRD § Explicit TBDs updated — items moved from "TBD" to "Decided" with pointers to where the decision lives.
- [ ] Decisions index (`docs/decisions.md`) updated if any ADRs are written.

## Blocked by

None — can start immediately.
