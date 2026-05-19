# ADR Template

This template applies to ADRs from **ADR-009 onward**. The earlier 8 ADRs (0001–0008) are frozen historical records and are not retroactively updated.

## Format

```markdown
# ADR-NNN: Title

**Status:** Accepted (YYYY-MM-DD)

**Supersedes:** [ADR-XXX](./XXXX-slug.md)  *(only when applicable)*

**Context.** What forces are at play. What constraints exist. What alternatives were considered.

**Decision.** What is being decided, in concrete and unambiguous terms.

**Consequences.** Positive (+) and negative (-) implications, including trade-offs the decision accepts.

**Verification.** Where the decision is enforced in code. One of:
- A specific test path: `tests/integration/idempotency_test.go::TestAllMutatingEndpointsRequireKey`
- A lint rule or type constraint
- `TBD — added at implementation` (for decisions whose enforcement is pending)
- `N/A — environmental/infra decision` (for choices where no in-code verification is possible)
```

## Why a `Verification` section?

Under AFK-agent dev with architectural-reviewer-only humans, ADRs are the load-bearing constraint on what agents can change unilaterally. An agent reading an ADR needs to know not only the decision but **where it lives in the code** — both to honour the decision and to extend it consistently.

Without a Verification entry, "every mutating endpoint accepts an `Idempotency-Key`" is a polite request. With it, the agent can see the enforcing test, write new endpoints that pass it, and trust that breaking the convention will fail CI.

## When to write an ADR

An ADR is justified when **all three** are true:

1. The decision is hard to reverse.
2. A future reader would wonder "why was it done this way?"
3. It is the result of a real trade-off (genuine alternatives existed).

If any of the three is absent, skip the ADR.

## Superseding an ADR

When a new ADR supersedes an old one:

- The **new** ADR's frontmatter includes `**Supersedes:** [ADR-XXX](./XXXX-slug.md)`.
- The **old** ADR's `**Status:**` line is changed to `Superseded by [ADR-YYY](./YYYY-slug.md) (YYYY-MM-DD)`. The body is left intact — it is historical record.
- The `docs/decisions.md` index reflects the superseded status in its right-hand column.
