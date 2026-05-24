# ADR-020: CI/CD — trunk-based, prod + staging, manual promotion to prod

**Status:** Accepted (2026-05-21) — amended by [ADR-027](./0027-phase-1-auto-deploy-direct-to-prod.md) (Phase 1 ships auto-deploy direct to prod; staging + manual-promote gate deferred)

**Context.** Phase 1 introduces three deployable artifacts (`cp-api`, `cp-ingest`, `dashboard`) plus Terraform-managed infrastructure. Before any of them ships, the team needs a settled story for: branch model, environment count, deployment promotion, artifact pattern, and Terraform-in-CI flow. These five sub-decisions interlock — the right branch model depends on the environment count, the right promotion strategy depends on whether a pre-prod env exists, the right artifact pattern depends on what runs where. Bundled into one ADR rather than five.

Two project constraints shape every sub-decision:

- **AFK-agent dev model with architectural-reviewer-only humans** (per `MEMORY.md`). PRs are short-lived, agent-authored, reviewer-approved. Long-lived feature branches don't fit.
- **Bounded blast radius of CP downtime in Phase 1.** Read-only operations against ~25 devices; a bad CP deploy stops the dashboard from updating but does not break devices. This makes single-prod *defensible* but not *preferred*.

**Decision.** Phase 1 ships with the following pipeline shape:

- **Branch model: trunk-based.** `main` is always deployable. PRs are short-lived (hours to a day), merge fast, no `develop` or release branches.
- **Environments: two — staging and prod.**
  - **staging**: single-AZ Postgres, smaller Fargate task sizes, separate AWS account or VPC. Hosts the Wave-0 bench device (`bench-mac-mini-01`) only.
  - **prod**: multi-AZ per ADR-015, full Fargate sizing. Hosts Waves 1–3 devices.
- **Deployment promotion: manual gate, with a documented switch criterion.** Merge to `main` auto-deploys to staging, runs smoke tests, and produces a `workflow_dispatch` "promote-to-prod" gate. An engineer reviews staging metrics and clicks promote. After **10 consecutive clean promotions** the runbook switches the gate to auto-promote. The "10 clean" criterion lives in `docs/runbooks/ci-cd.md`; the switch is itself a PR (one-line workflow change) so the decision is auditable.
- **Artifacts: three containers** — `cp-api`, `cp-ingest`, `dashboard`. All Fargate, all behind the ALB (the dashboard is server-rendered Next.js in a container, not a static S3+CloudFront export — paradigm parsimony with the rest of the stack matters more than the marginal cost savings of static hosting).
- **Terraform in CI:**
  - `terraform plan` on every PR against both environments, results commented on the PR.
  - `terraform apply` against staging on merge to `main`.
  - `terraform apply` against prod runs as part of the manual-promote (or eventually auto-promote) step.
  - State in S3 backend + DynamoDB lock table; CI assumes IAM roles via GitHub OIDC (no long-lived access keys).
- **Per-PR checks:** lint, type-check (Go + TS), unit tests, integration tests against testcontainers, Terraform plan, idempotency-CI-gate (per ADR-012), `scopedDeviceQuery`-CI-gate (per Issue 06).
- **Secrets:** CI assumes IAM via OIDC. The bootstrap key (ADR-017) is fetched from Secrets Manager only by the `mac-mini-rollout` CI, not CP's CI. CP's CI needs DB credentials for integration tests; those live in a CI-scoped Secrets Manager entry.

**Consequences.**

- (+) Trunk-based fits the AFK-agent + architectural-reviewer model. Short-lived PRs, no rebase pain on long branches.
- (+) Staging gives Wave 0 a true production-shaped environment to smoke against without exposing real client devices to in-progress code.
- (+) Manual gate keeps a human in the loop while the pipeline itself is being shaken out. The "10 clean" switch criterion makes "we keep the manual gate forever because nobody decided otherwise" impossible — there's a defined transition.
- (+) Three containers on Fargate = one observability story, one deploy story, one local-dev story (`docker compose`). Same paradigm as ADR-018's ingest worker call.
- (+) Terraform plan-on-PR catches infrastructure surprises before merge.
- (+) OIDC + scoped roles means no long-lived AWS keys in GitHub.
- (-) Two environments roughly double the always-on AWS spend vs single-prod. Staging's single-AZ Postgres + smaller Fargate sizing keep this ~30-40% of prod, not 100%. Accepted.
- (-) Manual gate slows velocity until the 10-clean threshold is crossed. Acceptable early in Phase 1.
- (-) Server-rendered dashboard in a container is more expensive than static S3+CloudFront (~$8/month for the Fargate task vs ~$1/month for static). Accepted for paradigm parsimony.
- (-) Trunk-based requires discipline around feature flags for in-progress work that can't ship-yet. Phase 1's slices are small enough that this is rarely needed; will revisit if it becomes painful.

**Verification.** `N/A — environmental/infra decision`. Verified operationally by:

- The presence of two environments in Terraform with the documented sizing differences.
- The PR check matrix matches the documented list (visible in CI workflow files).
- The runbook at `docs/runbooks/ci-cd.md` documents the manual-gate criterion and the 10-clean switch trigger.
- The `mac-mini-rollout` CI assumes only the bootstrap-key-read IAM role; CP's CI does not have access to that secret.
