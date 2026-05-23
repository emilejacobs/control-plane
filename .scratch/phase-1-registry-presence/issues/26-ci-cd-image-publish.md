# Issue 26 — CI/CD slice 1: build + push container images to ECR

Status: in-progress
Type: AFK

## Parent

- ADR: [`docs/adr/0020-ci-cd-trunk-based-staging-manual-promote.md`](../../../docs/adr/0020-ci-cd-trunk-based-staging-manual-promote.md) — the CI/CD shape this slice begins implementing.
- Bottleneck cited in: handoff document from prior session (after #25 landed). Until real images exist in the three ECR repos, `cp-ingest` + `tailscale-subnet-router` stay at `desired_count = 0` and `cp-api` + dashboard ECS task defs run nginx placeholders.
- Issue split: ADR-020 specifies a full pipeline (staging + prod, plan-on-PR, manual promote gate, idempotency + scopedDeviceQuery CI gates). This issue is the first vertical slice — image publishing only. Subsequent issues will layer on auto-deploy, staging env, plan-on-PR, manual promote, and the CI gates.

## What to build

A GitHub Actions workflow + per-service `Dockerfile`s that, on merge to `main`, build and push three container images to the ECR repos created by #25:

- `uknomi/cp-api` ← `cmd/cp-api` (Go service, multi-stage build, distroless or alpine final image)
- `uknomi/cp-ingest` ← `cmd/cp-ingest` (Go service, same build pattern)
- `uknomi/dashboard` ← `web/` (Next.js, server-rendered per ADR-020 — `next start` in a Node container, not static export)

Tag scheme: `<git-sha>` plus `latest`. Pushing `latest` is acceptable in this slice because deploy still happens via the operator (`terraform apply`); a future slice introduces the auto-deploy of ECS task defs, at which point `latest` may be dropped.

Auth: GitHub OIDC federation. A new IAM role in `infra/terraform-deploy/` trusts the GH OIDC provider, scoped to this repo + `main` branch only. The role grants `ecr:GetAuthorizationToken` + per-repo `ecr:BatchGetImage`, `ecr:PutImage`, `ecr:InitiateLayerUpload`, `ecr:UploadLayerPart`, `ecr:CompleteLayerUpload`, `ecr:BatchCheckLayerAvailability`.

Two small follow-ups from #25 are folded in (each is necessary to make the image actually deployable, so splitting them off would be churn):

- **cp-api `/healthz` handler.** Returns 200 with no body. Lets the ALB target group matcher tighten from `200-499` to `200` (also touched here; the comment in `infra/terraform-deploy/cp-api.tf` is removed).
- **dashboard `NEXT_PUBLIC_API_URL` build-time bake.** Next.js bakes `NEXT_PUBLIC_*` into the JS bundle at `next build` time, so the Dockerfile must accept it as a build arg and the workflow must pass `https://api.control.uknomi.com`.

## Out of scope

- Auto-redeploy of ECS services on image push (future slice).
- Staging environment (future slice — ADR-020 calls for it but the deploy root is currently single-env; introducing staging is its own restructuring).
- `terraform plan` on PR (future slice — needs a separate read-only OIDC role + comment-bot).
- Manual `promote-to-prod` workflow_dispatch gate + the 10-clean switch criterion (future slice; depends on staging).
- Idempotency-CI-gate (per ADR-012) and scopedDeviceQuery-CI-gate (per Issue 06). Separate slices.
- `db-dsn` secret rotation handling (handoff follow-up; needs cp-api/cp-ingest config refactor — separate slice).
- `mac-mini-rollout` repo's bootstrap-key-baking workflow (lives in the sister repo's tracker — #10 AC2).

## Acceptance criteria

- [ ] `cmd/cp-api` exposes `GET /healthz` returning 200, covered by a Go unit test.
- [ ] `Dockerfile` exists for `cp-api`, `cp-ingest`, and `web/` (dashboard). Each builds locally (`docker build .`) without secrets.
- [ ] Final images run as a non-root user, contain no build toolchain, and are based on minimal images (distroless or alpine for Go; `node:LTS-alpine` runtime for dashboard).
- [ ] Dashboard `Dockerfile` accepts `NEXT_PUBLIC_API_URL` as a build arg and bakes it into the static bundle.
- [ ] `.github/workflows/build-images.yml` (or equivalent) triggers on push to `main`, authenticates to AWS via OIDC, and pushes the three images tagged with the git SHA + `latest`.
- [ ] `infra/terraform-deploy/` gains:
  - GitHub OIDC identity provider (or references the account-level one if one already exists).
  - An IAM role assumable from `repo:uknomi/uknomi-control-plane:ref:refs/heads/main` (sub-claim scoped, not wildcarded across the repo) with the ECR push permissions enumerated above.
  - Outputs the role ARN.
- [ ] `cp-api.tf`'s ALB health-check matcher tightens from `200-499` to `200`; the explanatory comment is updated/removed.
- [ ] `terraform fmt + validate` pass on both roots.
- [ ] `go test ./...` passes; `web/` vitest suite passes; `web/` `next build` succeeds with the build arg set.
- [ ] **Documentation updated.** `docs/architecture.md` § Cloud infrastructure mentions the image build/push flow; `infra/terraform-deploy/README.md` notes the OIDC role; consider a new ADR only if a load-bearing decision warrants it (likely not for this slice — ADR-020 already covers the shape).

## Blocked by

- None — #25 created the ECR repos and the deploy root, so this slice can land standalone.

## Notes

- The user-direction memory from the handoff is unchanged: account `523612763411`, region `us-east-1`. The OIDC subject claim must match the repo's canonical GitHub path (confirm via `git remote -v` before the role is applied).
- TDD memories apply to the Go work (cp-api `/healthz`). The Dockerfile + workflow work is structural, not test-driven in the same sense; verification is local build + `terraform validate`.
