# Terraform — Phase 1 deployment infra (#25)

The Phase 1 deployment root. Sibling to `infra/terraform/` (the IoT Core + bootstrap-key root from #01 + #10); the two roots share the same S3 state bucket under different keys.

Scope of the eventual root: VPC + networking, RDS Postgres, ECS Fargate (`cp-api`, `cp-ingest`, `dashboard`, `tailscale-subnet-router`), ALB + ACM + Route 53, KMS, S3 buckets (audit mirror, command output, agent distribution), ECR repos, IAM roles, IoT rules wiring (instantiates `infra/terraform/modules/sqs-ingest` + `modules/cp-ingest-service`), CloudWatch alarms + SNS. Tracked in [issue #25](../../.scratch/phase-1-registry-presence/issues/25-phase-1-deployment-infra.md).

## Status

Landing incrementally per #25's 14-step staging order.

| Step | Slice | Status |
|---|---|---|
| 1 | Networking — VPC, subnets, IGW + single NAT, route tables, VPC endpoints, SGs | **built** |
| 2 | State backend `key` + bootstrap doc | **built** (see [§ State](#state)) |
| 3 | KMS + Secrets Manager | **built** (see [§ KMS + Secrets](#kms--secrets)) |
| 4 | RDS Postgres | **built** |
| 5 | ECR repos | **built** |
| 6 | ECS cluster + execution role + log groups | **built** |
| 7 | Task roles per service | **built** |
| 8 | `cp-api` task + service + ALB + ACM + Route 53 | **built** |
| 9 | `dashboard` task + service | **built** |
| 10 | `cp-ingest` task + service + IoT rules wiring | **built** |
| 11 | Tailscale subnet router | **built** |
| 12 | S3 buckets | **built** |
| 13 | CloudWatch alarms + SNS | **built** |
| 14 | Docs + ADRs | **built** (ADR-022, `architecture.md` updated) |

Follow-on slices (tracked separately):

| Issue | Slice | Status |
|---|---|---|
| #26 | CI/CD slice 1 — image build/push to ECR via OIDC | **built** |
| #27 | Image-ref flip — services pull from ECR | **built** |

## Workflow

```bash
cd infra/terraform-deploy
terraform init    # first time, after the state-backend bootstrap (one-time)
terraform plan
terraform apply
```

The state-backend bootstrap (creating the S3 bucket + DynamoDB lock table) is documented in [`../terraform/README.md`](../terraform/README.md) § "State backend bootstrap" — run those commands once before this root's first `init`.

## State

S3 backend on `uknomi-tfstate-523612763411` with DynamoDB locking on `uknomi-tfstate-locks`. This root's key is `deploy/terraform.tfstate`; the IoT Core root sits beside it at `iot-core/terraform.tfstate`.

## Phase 1 decisions (recorded 2026-05-22)

1. **Region / account:** `us-east-1` / `523612763411`.
2. **DNS:** new Route 53 hosted zone `control.uknomi.com` (created in the ALB / ACM / Route 53 slice). Proposed hostname split: dashboard at `control.uknomi.com` (apex), cp-api at `api.control.uknomi.com`; the ACM cert carries both as SANs and the ALB does host-based routing. A one-time NS delegation at the registrar of `uknomi.com` is required for the new zone to resolve publicly.
3. **Tailscale:** existing tailnet, non-expiring auth key. The key lands in Secrets Manager in the secrets slice and is read by the subnet-router task at startup.
4. **Image source for v0:** public placeholders (e.g. `public.ecr.aws/nginx/nginx-unprivileged:latest`) for `cp-api` and `dashboard` so the ALB has healthy targets on initial apply. `cp-ingest` and `tailscale-subnet-router` start at `desired_count = 0`. Real images land via #02 (CI pipeline).

## KMS + Secrets

The root provisions a single customer-managed KMS key (`alias/uknomi-cp`) used for at-rest encryption by Secrets Manager (in this slice), RDS (step 4), and S3 (step 12). The key policy allows IAM identities in this account full management; service principals get only the operations they need (Secrets Manager today, RDS / S3 in later slices). Key rotation is enabled.

Three Secrets Manager secrets are created with non-secret placeholders; the real values are set out-of-band, like #10's bootstrap key:

```bash
# The cp-api JWT signing key — base64 of >= 32 raw bytes.
aws secretsmanager put-secret-value \
  --secret-id uknomi/cp/jwt-signing-key \
  --secret-string "$(openssl rand -base64 48)"

# The TOTP-at-rest encryption key — base64 of exactly 32 raw bytes.
aws secretsmanager put-secret-value \
  --secret-id uknomi/cp/totp-encryption-key \
  --secret-string "$(openssl rand -base64 32)"

# The non-expiring Tailscale auth key for the existing tailnet (generate it in
# the Tailscale admin console with the "reusable" + "no expiry" flags).
aws secretsmanager put-secret-value \
  --secret-id uknomi/cp/tailscale-auth-key \
  --secret-string "tskey-auth-..."
```

`lifecycle { ignore_changes = [secret_string] }` on the version resource keeps Terraform off the real value after the placeholder gets replaced. The Fargate task definitions (steps 8 / 11) reference these secrets by ARN; the secret values are injected as env vars at task start time, never landing in any artefact.

The mac-mini-rollout install-package bootstrap key from #10 (`uknomi/cp/bootstrap-key`) follows the same pattern but lives in the IoT Core root next door for historical reasons — moving it would require cross-root state migration. Set its value the same way.

## This slice — networking (step 1)

- One VPC (`10.0.0.0/16`), two AZs, one public + one private subnet per AZ.
- One Internet Gateway, one NAT Gateway in AZ-0 (cost posture; promote to per-AZ NAT when traffic demands).
- Two route tables: public → IGW, private → NAT.
- Gateway VPC endpoints for S3 + DynamoDB (free).
- Interface VPC endpoints for Secrets Manager, ECR (API + DKR), CloudWatch Logs — the AWS APIs the CP services hit frequently. SQS deliberately not endpointed; cp-ingest's poll traffic goes through NAT.
- Five security groups: `alb` (HTTPS from anywhere), `tasks` (8080 + 3000 from `alb`), `rds` (5432 from `tasks`), `tailscale` (egress only), `vpc-endpoints` (443 from `tasks`).

`terraform fmt + validate` clean. `terraform apply` not run from this checkout — applies are deploy events.

## CI/CD role: image publish + auto-deploy (Issue #26 + ADR-027)

`ci-oidc.tf` provisions the GitHub Actions OIDC provider + an IAM role (`uknomi-gha-image-publish`) trusted only by `repo:emilejacobs/control-plane:ref:refs/heads/main`. The `.github/workflows/build-images.yml` workflow assumes this role on every merge to `main`. Two inline policies hang off the role:

- **`image-publish`** (Issue #26): push the four CP images (`cp-api`, `cp-ingest`, `dashboard`, `audit-mirror`) to ECR tagged with the git SHA + `latest`.
- **`deploy`** (ADR-027): `ecs:UpdateService` + `wait services-stable` on the three long-running services, `ecs:RunTask` + `iam:PassRole` on audit-mirror, and `events:ListTargetsByRule` to re-derive the audit-mirror network config at workflow runtime.

The workflow uses `dorny/paths-filter` to skip rebuilds + redeploys for unaffected services — a docs-only commit does not roll any ECS task; a `web/**`-only commit rolls only the dashboard. The affected set drives a **dynamic build/deploy matrix**, so an unaffected service produces no job (not a green no-op), one service's build failure does not skip another's deploy, and a leg whose `:<sha>` image never reached ECR fails instead of rolling a stale `:latest` (issue #11 — see [ADR-027](../../docs/adr/0027-phase-1-auto-deploy-direct-to-prod.md) § Amendment).

If the AWS account already has a `token.actions.githubusercontent.com` OIDC provider, `terraform apply` fails with `EntityAlreadyExists`. Recover via:

```bash
terraform import aws_iam_openid_connect_provider.github \
  arn:aws:iam::523612763411:oidc-provider/token.actions.githubusercontent.com
```

The role ARN is in the `gha_image_publish_role_arn` output — the workflow hardcodes it via `env.OIDC_ROLE_ARN` because workflows cannot read TF outputs directly. Update both if the role is ever renamed.

## Deploying the CP (Issue #27 + ADR-027)

The task definitions reference ECR via `${repo}:${var.image_tag}`. `image_tag` defaults to `"latest"`, so a vanilla `terraform apply` picks up whatever the build-images workflow most recently pushed.

**Normal flow (per ADR-027):** merge to `main` is the entire deploy. The workflow builds the affected images, pushes them to ECR, then issues `aws ecs update-service --force-new-deployment` + `aws ecs wait services-stable` for each affected long-running service. Audit-mirror gets `aws ecs run-task` for an immediate smoke run; the existing `uknomi-cp-audit-mirror-failure` alarm catches a non-zero exit.

**Order of operations for the first-ever deploy:**

1. `terraform apply` once — provisions the OIDC role + ECR repos. The task defs reference images that do not exist yet, so ECS service creation succeeds but tasks fail to start. Expected.
2. Push a commit to `main` (or trigger `.github/workflows/build-images.yml` via `workflow_dispatch`). Wait ~3–5 min for the build matrix; another ~1–2 min for the deploy matrix to mark services stable.
3. Done. Subsequent merges follow the normal flow above — no manual `terraform apply` needed unless a task-def field actually changed.

**Manual rollout (escape hatch):**

Useful when redeploying without an image change (e.g. to pick up a Secrets Manager rotation) or when CI is down:

```bash
aws ecs update-service --cluster uknomi-cp --service cp-api      --force-new-deployment
aws ecs update-service --cluster uknomi-cp --service cp-ingest   --force-new-deployment
aws ecs update-service --cluster uknomi-cp --service dashboard   --force-new-deployment
```

**Pin a specific SHA (rollback):**

```bash
terraform apply -var image_tag=7af89d8
```

Same command with the previous SHA to undo a bad roll. Because all three long-running services share one `image_tag` they roll back together — the simple case for the AFK-agent dev model where the CP is cut from one commit. `var.image_tag` pinning takes precedence over the workflow's auto-deploy; the workflow's `update-service --force-new-deployment` pulls whatever `:latest` currently points to, but a pinned `image_tag` reverts the task def to the explicit SHA on the next `terraform apply`.

**Mismatched versions per service:**

Rare, but if needed, apply with `-target` against the specific task-def + service. A future slice may split `image_tag` into per-service variables; not worth the API surface today.

**Tailscale + secret-gated services:**

`tailscale-subnet-router` runs the public `tailscale/tailscale:stable` image and stays at `desired_count = 0` until the operator sets the real `uknomi/cp/tailscale-auth-key` Secrets Manager value (see § KMS + Secrets above). Same for `cp-api` — it will not start cleanly until the JWT signing key + TOTP encryption key are real (not the Terraform-managed placeholders).
