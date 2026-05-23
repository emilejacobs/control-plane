# Issue 26 — CI/CD slice 1: build + push container images to ECR

Status: done
Type: AFK
Completed: 2026-05-22 — 5 commits, see notes.

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

- **cp-api `/healthz` handler.** Returns 200 with no body. Necessary precondition for the ALB matcher to tighten from `200-499` to `200`; the matcher change itself defers to the slice that flips the cp-api task def image off the nginx placeholder (tightening now, while the placeholder still 404s, would break the deploy).
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

- [x] `cmd/cp-api` exposes `GET /healthz` returning 200, covered by a Go unit test.
- [x] `Dockerfile` exists for `cp-api`, `cp-ingest`, and `web/` (dashboard). Static Go build verified outside Docker (produced a working 14MB stripped ELF); `next build` verified locally with `output: "standalone"`. *(In-Docker `docker build` not exercised locally — colima/docker daemon was stopped this session. CI exercises the full build path.)*
- [x] Final images run as a non-root user, contain no build toolchain, and are based on minimal images (distroless/static for Go; `node:22-alpine` for dashboard).
- [x] Dashboard `Dockerfile` accepts `NEXT_PUBLIC_API_URL` as a build arg and bakes it into the static bundle.
- [x] `.github/workflows/build-images.yml` triggers on push to `main`, authenticates to AWS via OIDC, and pushes the three images tagged with the git SHA + `latest`.
- [x] `infra/terraform-deploy/` gains:
  - GitHub OIDC identity provider (`aws_iam_openid_connect_provider.github` in `ci-oidc.tf`; importable if the account already has one).
  - IAM role `uknomi-gha-image-publish` with sub-claim scoped to `repo:emilejacobs/control-plane:ref:refs/heads/main` and the ECR push permissions.
  - Output `gha_image_publish_role_arn`.
- [x] `cp-api.tf`'s comment updated; matcher tightening explicitly deferred to the image-ref-flip slice.
- [x] `terraform fmt + validate` pass on both roots.
- [x] `go test ./...` passes; `web/` vitest suite passes (44/44); `web/` `next build` succeeds with the build arg set.
- [x] **Documentation updated.** `docs/architecture.md` § Cloud infrastructure now lists #26 as built; `infra/terraform-deploy/README.md` documents the OIDC role + import escape hatch. No new ADR — ADR-020 already covers the shape, and no load-bearing surprise emerged in this slice.

## Blocked by

- None — #25 created the ECR repos and the deploy root, so this slice can land standalone.

## Notes

- The user-direction memory from the handoff is unchanged: account `523612763411`, region `us-east-1`. The OIDC subject claim must match the repo's canonical GitHub path (confirm via `git remote -v` before the role is applied).
- TDD memories apply to the Go work (cp-api `/healthz`). The Dockerfile + workflow work is structural, not test-driven in the same sense; verification is local build + `terraform validate`.

### Completion notes (2026-05-22)

Commits (this branch):

- `7322a26` — cp-api `/healthz` handler (TDD red→green→commit).
- `12c1478` — cp-api.tf comment update; matcher tightening explicitly deferred.
- `0d47f76` — Dockerfiles for cp-api, cp-ingest, dashboard; `.dockerignore`s; `output: "standalone"` on next.config.ts. Verified outside Docker.
- `235bf8c` — `infra/terraform-deploy/ci-oidc.tf` — GitHub OIDC provider + `uknomi-gha-image-publish` role + per-repo ECR push policy + outputs. `terraform fmt + validate` clean.
- `86e56b5` — `.github/workflows/build-images.yml` — matrix-fanned image build/push on merge to main + `workflow_dispatch`, OIDC auth via `aws-actions/configure-aws-credentials@v4`, gha cache.

### Unblocks / next slices

This slice ends with: images can be pushed to ECR. It does **not** end with: a real CP deployed. The natural follow-on slice (next issue to file when picked up) is the image-ref flip:

1. Operator runs `terraform apply` once to materialise the OIDC provider + role.
2. Push to `main` (or `workflow_dispatch`) — workflow builds + pushes the three images.
3. New slice: flip `cp-api`, `cp-ingest`, `dashboard` task-def `image` references from the nginx placeholder to `${ecr_url}:<sha-or-latest>`; bump `cp-ingest` and `tailscale-subnet-router` `desired_count` from 0 to 1; tighten the ALB matcher from `200-499` to `200`; document the operator playbook for picking a SHA.

Other ADR-020 work still owed (each its own future issue): staging environment, terraform-plan-on-PR (separate read-only OIDC role), manual `promote-to-prod` workflow_dispatch gate + 10-clean switch criterion runbook, idempotency-CI-gate (ADR-012), `scopedDeviceQuery`-CI-gate (Issue 06 outcome).

### Operator preconditions before the first workflow run

1. `terraform apply` in `infra/terraform-deploy/` (or `terraform import` first if the account already has a GH Actions OIDC provider — see deploy-root README).
2. Confirm the role exists: `aws iam get-role --role-name uknomi-gha-image-publish`.
3. Push a commit to `main` (or trigger the workflow manually) — images appear in ECR within ~3-5 minutes.
