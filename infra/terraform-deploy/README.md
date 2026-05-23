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
