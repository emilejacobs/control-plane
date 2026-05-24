# ADR-027: Phase 1 auto-deploy goes direct to prod; staging + manual promote gate deferred

**Status:** Accepted (2026-05-24)

**Amends:** [ADR-020](./0020-ci-cd-trunk-based-staging-manual-promote.md)

**Context.** ADR-020 specified the full long-term CI/CD shape: trunk-based, two environments (staging + prod), `terraform apply` to staging on merge to `main`, plus a `workflow_dispatch` "promote-to-prod" gate that flips to auto-promote after 10 consecutive clean rolls. None of that is in place yet. What does exist (from Issues #25–#27):

- `infra/terraform-deploy/` provisions a single prod environment. No staging account, no staging VPC, no staging RDS.
- `.github/workflows/build-images.yml` pushes the four CP images (`cp-api`, `cp-ingest`, `dashboard`, `audit-mirror`) to ECR on merge to `main`. Tags pushed: `<git-sha>` and `:latest`.
- All four Terraform-managed task definitions pin `${ecr_url}:${var.image_tag}` where `var.image_tag` defaults to `"latest"`.
- The operator runs `aws ecs update-service --cluster uknomi-cp --service <svc> --force-new-deployment` by hand after each merge that needs to ship. The 2026-05-24 Wave-0 session alone triggered three manual rolls (cp-api twice, dashboard once).

Two real options for closing the manual-roll gap:

1. **Stand up the full ADR-020 shape now.** New AWS account or VPC for staging, second RDS, second ALB+ACM+DNS pair (`staging.control.uknomi.com`), duplicated alarms, plan-on-PR wired up, the manual promote gate with the documented 10-clean criterion. Multi-week slice. Doubles always-on infra cost during the buildout. Right answer eventually; not the right answer for the next 1-2 day slice.
2. **Auto-deploy direct to prod for Phase 1.** Extend `build-images.yml` to `aws ecs update-service --force-new-deployment` (and `wait services-stable`) after each image push succeeds. Same single-prod posture as today, just without the manual step. Path-filter per service so a docs-only or single-service change does not churn unrelated tasks. Audit-mirror gets `aws ecs run-task` to verify the fresh image before the next 00:05 UTC scheduled run.

Option 2 deviates from ADR-020's "staging + manual promote" intent for the duration of Phase 1. That deviation is defensible right now because:

- **Blast radius is bounded.** Wave 0 is a single bench device. ADR-022 already accepts a single-prod posture for Phase 1 deployment infra; ADR-018 keeps the ingest path resilient to a brief cp-api outage. A bad roll degrades the dashboard / cp-api but does not break the field agents (IoT Core decouples device-side connectivity from cp-api availability).
- **It matches what the operator already does.** Manual `--force-new-deployment` after merge is the existing path. Auto-deploy replaces a manual step that was always going to run; it does not introduce a new failure mode that the manual flow lacked.
- **ECS's deployment circuit-breaker** rolls back at the task level if the new task fails to reach steady state. Combined with `wait services-stable` in the workflow, a bad image surfaces as a CI failure within minutes, not as a silent rollout.
- **CI already runs the full Go + vitest suite per PR** (per [ADR-012](./0012-test-policy.md)). The "shake out in staging" value ADR-020 cites is partially provided by tests; the remaining residual is real but bounded at Wave 0 scale.

The cost is real: a test that depends on something the test suite cannot exercise (e.g. the IAM `iot:AttachPolicy` gap caught in the 2026-05-24 session, or the lifecycle-rule `correlation_id` gap from the same session) can ship to prod with no pre-prod buffer. The 2026-05-24 session's defects were caught by the Wave 0 smoke runbook against prod, not by a staging gate. That smoke runbook continues to be the human checkpoint for the Wave-1 → Wave-3 transition.

**Decision.** Phase 1 ships auto-deploy directly to prod from `main`. The `build-images.yml` workflow is extended so that each per-service image build is followed by:

- `aws ecs update-service --cluster uknomi-cp --service <svc> --force-new-deployment` for `cp-api`, `cp-ingest`, `dashboard`.
- `aws ecs wait services-stable --cluster uknomi-cp --services <svc>` so the workflow fails loudly if the new task does not reach steady state.
- `aws ecs run-task` for `audit-mirror` (fire-and-forget; the existing `audit-mirror-failure` alarm catches a non-zero exit).
- Per-service path filters so unrelated changes (docs, scratch issues, infra/* outside the build context) do not roll any service. A `web/**`-only change rolls only the dashboard; a `cmd/cp-api/**`-only change rolls only cp-api; a shared-internal-package change rolls all three Go services. Filters are conservative — when in doubt, deploy.

ADR-020's staging + manual-promote shape remains the **stated target** for the Phase 1 → Phase 2 transition or the moment the fleet outgrows the bench-smoke posture (whichever comes first). This ADR explicitly defers it, not abandons it.

**Consequences.**
- (+) Closes the manual-roll gap. Merge → ECR push → ECS update is a single workflow run; the operator no longer has to remember `update-service --force-new-deployment`.
- (+) ECS circuit-breaker + `wait services-stable` give the workflow a meaningful pass/fail signal. A failing roll surfaces as a red CI run, not as a silent half-deploy.
- (+) Path filters keep deploy churn proportional to the change. Docs commits do not roll prod; web-only commits do not touch cp-api.
- (+) Aligns Phase 1's CI/CD posture with [ADR-022](./0022-phase-1-deployment-shape.md) (single-prod) and [ADR-025](./0025-direct-to-main-pushes-under-afk-agent-dev.md) (direct-to-main pushes). All three ADRs share the same root: the AFK-agent dev model + Wave-0 blast radius justify a leaner posture than the eventual target.
- (+) Reduces the diff between "what the runbook says" and "what actually happens" — `infra/terraform-deploy/README.md` no longer needs a "now run `aws ecs update-service`" footnote.
- (-) A bad deploy reaches prod without any pre-prod buffer. Mitigated by ECS circuit-breaker, by `wait services-stable`, by the existing CI gate suite, and by the Wave-0 smoke runbook for waves themselves. Not mitigated against bugs the test suite cannot reach.
- (-) Path filters add per-service complexity to `build-images.yml`. Trade-off accepted: explicit deploy mapping is easier to reason about than "always deploy everything".
- (-) ADR-020's "10 clean promotions" switch criterion does not apply here — there is no manual gate to count clean runs against. When staging is eventually stood up, the counter starts fresh (and may be reframed as "10 clean staging→prod auto-promotions" instead).

**Verification.**
- `.github/workflows/build-images.yml` contains a `deploy` job per service with `aws ecs update-service --force-new-deployment` + `aws ecs wait services-stable`, gated on the per-service path filter from `dorny/paths-filter`.
- `.github/workflows/build-images.yml` contains an `audit-mirror` run-task step gated on the audit-mirror path filter.
- `infra/terraform-deploy/ci-oidc.tf` grants the `uknomi-gha-image-publish` role the `ecs:UpdateService` + `ecs:DescribeServices` + `ecs:RunTask` + `iam:PassRole` permissions scoped to the four CP services + their task roles, and `events:ListTargetsByRule` for audit-mirror network-config discovery.
- `infra/terraform-deploy/README.md` § Deploying the CP documents the new flow: merge to `main` rolls the affected services automatically; manual `--force-new-deployment` remains a runbook escape hatch for redeploying without an image change.
