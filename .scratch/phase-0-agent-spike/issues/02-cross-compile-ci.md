# Issue 02 — Cross-compile build matrix + CI gate

Status: done

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

A CI workflow (GitHub Actions or equivalent — pick at implementation time) that proves ADR-002's "same Go codebase, three platforms" claim and enforces ADR-012's CI-gate requirement.

Scope:

- Cross-compile the agent and `agent-cli` binaries for `darwin/arm64`, `darwin/amd64`, `linux/arm64` (per ADR-002).
- Run `go test ./...` on each platform's tests (matrix job).
- Gate merge to `main` on all matrix rows being green (per ADR-012). This is the **primary safety net** under AFK-agent dev — see ADR-012 context.
- Do **not** exercise real AWS IoT Core in CI (cost/coverage trade-off documented in the PRD's Testing Decisions and Out of Scope).
- Emit the cross-compiled binaries as CI artefacts on successful build so the Phase 0 field deployments (Issues 07, 08) can download them directly.

## Acceptance criteria

- [ ] CI runs on every PR and on pushes to `main`.
- [ ] Matrix has three rows: `darwin/arm64`, `darwin/amd64`, `linux/arm64`.
- [ ] Each row builds both the agent and `agent-cli` and runs the full test suite.
- [ ] A failing test in any row blocks merge (branch protection rule or equivalent merge guard).
- [ ] Deliberately breaking a test in a draft PR demonstrates that merge is blocked.
- [ ] CI artefacts on a green run include the three compiled binaries, downloadable for field deployment.

## Blocked by

- [Issue 01 — First light](./01-first-light.md)
