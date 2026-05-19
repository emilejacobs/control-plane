# Issue 09 — Populate ADR Verification fields (002, 011, 012)

Status: ready-for-agent

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

Update the `**Verification.**` field of the three ADRs whose implementation began in Phase 0. Per `docs/agents/adr-template.md`, every ADR from ADR-009 onward carries a Verification entry pointing at how the decision is enforced in code; ADR-002 also needs one even though it predates the template, because its Verification has become concrete with Phase 0.

Scope:

- **ADR-002** (Go agent, single cross-compiled binary): currently has no Verification entry (predates the template). Add one pointing at the CI build matrix configuration created in Issue 02.
- **ADR-011** (Structured JSON logs + correlation IDs): Verification currently says `TBD — added at implementation`. Replace with concrete references to:
  - The envelope schema test (asserts `correlation_id` is required).
  - The log-shape lint or convention check, if implemented.
  - The integration test that asserts `correlation_id` propagates from request envelope → log lines → response envelope through a command lifecycle.
- **ADR-012** (Test policy + CI gate): Verification currently says `TBD — added at implementation`. Replace with concrete references to:
  - The CI gate configuration that blocks merge on test failure.
  - The dispatcher/transport test directories.
  - The handler-coverage check (if implemented; otherwise note as a follow-up).

Format per `docs/agents/adr-template.md`: a path to the test (`<dir>/<file>_test.go::TestName`) or to the CI config file. Multiple references are fine if listed clearly.

## Acceptance criteria

- [ ] ADR-002 Verification field added (it currently has none) and points at the CI build matrix.
- [ ] ADR-011 Verification field updated; no longer says `TBD`.
- [ ] ADR-012 Verification field updated; no longer says `TBD`.
- [ ] Each referenced test path or config path exists in the repo and is what the ADR claims it is.
- [ ] `decisions.md` index does not need changes (status of each ADR is unchanged — Accepted remains Accepted).

## Blocked by

- [Issue 01 — First light](./01-first-light.md)
- [Issue 02 — Cross-compile + CI](./02-cross-compile-ci.md)
