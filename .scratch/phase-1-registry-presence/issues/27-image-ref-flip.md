# Issue 27 — Image-ref flip: drop the nginx placeholders

Status: in-progress
Type: AFK

## Parent

- [Issue #26](./26-ci-cd-image-publish.md) — built the pipeline that pushes real images to ECR. This slice consumes those images.
- ADR-022 — left the placeholders + `desired_count = 0` as an explicit Wave-0 cost posture; this slice flips both as the natural next step.

## What to build

Replace the three `public.ecr.aws/nginx/nginx-unprivileged:latest` placeholders in `infra/terraform-deploy/` with the ECR-hosted CP images, bump `cp-ingest` from `desired_count = 0` to `1`, fix the dashboard's container port (8080 → 3000), and tighten the ALB matchers from the placeholder-tolerant ranges to `200` only.

Scope:

- `cp-api.tf` — task def `image` flips to `${ecr_url}:${var.image_tag}`; target group matcher tightens from `200-499` to `200`.
- `cp-ingest.tf` — module `image` arg flips; service `desired_count` 0 → 1.
- `dashboard.tf` — task def `image` flips; task def `portMappings.containerPort` 8080 → 3000; service `load_balancer.container_port` 8080 → 3000; target group `port` 8080 → 3000; target group matcher tightens from `200-399` to `200`.
- A new shared variable `var.image_tag` defaulting to `"latest"` so operators can pin to a specific SHA per apply without code edits.
- A new section in `infra/terraform-deploy/README.md` documenting the deploy playbook: workflow runs first (images must exist in ECR), `terraform apply` picks up `:latest` by default, pin with `-var image_tag=<sha>`, rollback = re-apply with previous SHA.

Out of scope:

- **Tailscale subnet router.** Uses the public `tailscale/tailscale:stable` image, not ECR. Its `desired_count = 0` stays — gated on the operator setting the real Tailscale auth-key secret value (separate manual step).
- **Auto-redeploy of ECS services on image push.** This slice still requires the operator to run `terraform apply` after each workflow run; a separate follow-on can teach the workflow to call `aws ecs update-service --force-new-deployment` (or move the image tag through SSM Parameter Store).
- **Staging environment** and the rest of ADR-020 (manual promote gate, plan-on-PR, idempotency + scopedDeviceQuery CI gates).

## Acceptance criteria

- [ ] All three CP services reference ECR images via `${aws_ecr_repository.main[<service>].repository_url}:${var.image_tag}`. No more nginx placeholders.
- [ ] `cp-ingest` service `desired_count = 1`.
- [ ] Dashboard container port + target group port + service load_balancer container_port are all `3000`.
- [ ] `cp-api` ALB matcher = `"200"`. Dashboard ALB matcher = `"200"`.
- [ ] `var.image_tag` exists with `default = "latest"`, documented.
- [ ] `infra/terraform-deploy/README.md` carries a "Deploying the CP" section: workflow → apply, how to pin, how to roll back, the gotcha that ECR must be populated before the first apply touches the task defs.
- [ ] `terraform fmt + validate` clean.
- [ ] `docs/architecture.md` § Cloud infrastructure updated — the "until the image-flip slice" framing is replaced with the current state.

## Blocked by

- None on the code side. The deploy itself depends on the workflow from #26 having pushed images at least once.

## Notes

- One shared `var.image_tag` rather than per-service tags. The CP is cut from one git commit; deploying mismatched service versions is rare enough to belong behind a `-target` apply when needed.
- Migrations: both `cp-api` and `cp-ingest` call `storage.Migrate` on startup. Goose holds an advisory lock so concurrent migrators serialize without conflict; no work needed here.
