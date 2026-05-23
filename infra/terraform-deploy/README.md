# Terraform — Phase 1 deployment infra (#25)

The Phase 1 deployment root. Sibling to `infra/terraform/` (the IoT Core + bootstrap-key root from #01 + #10); the two roots share the same S3 state bucket under different keys.

Scope of the eventual root: VPC + networking, RDS Postgres, ECS Fargate (`cp-api`, `cp-ingest`, `dashboard`, `tailscale-subnet-router`), ALB + ACM + Route 53, KMS, S3 buckets (audit mirror, command output, agent distribution), ECR repos, IAM roles, IoT rules wiring (instantiates `infra/terraform/modules/sqs-ingest` + `modules/cp-ingest-service`), CloudWatch alarms + SNS. Tracked in [issue #25](../../.scratch/phase-1-registry-presence/issues/25-phase-1-deployment-infra.md).

## Status

Landing incrementally per #25's 14-step staging order.

| Step | Slice | Status |
|---|---|---|
| 1 | Networking — VPC, subnets, IGW + single NAT, route tables, VPC endpoints, SGs | **built** (this commit) |
| 2 | State backend `key` + bootstrap doc | **built** (this commit — see [§ State](#state)) |
| 3 | KMS + Secrets Manager | pending |
| 4 | RDS Postgres | pending |
| 5 | ECR repos | pending |
| 6 | ECS cluster + execution role + log groups | pending |
| 7 | Task roles per service | pending |
| 8 | `cp-api` task + service + ALB + ACM + Route 53 | pending |
| 9 | `dashboard` task + service | pending |
| 10 | `cp-ingest` task + service + IoT rules wiring | pending |
| 11 | Tailscale subnet router | pending |
| 12 | S3 buckets | pending |
| 13 | CloudWatch alarms + SNS | pending |
| 14 | Docs + ADRs | pending |

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

## This slice — networking (step 1)

- One VPC (`10.0.0.0/16`), two AZs, one public + one private subnet per AZ.
- One Internet Gateway, one NAT Gateway in AZ-0 (cost posture; promote to per-AZ NAT when traffic demands).
- Two route tables: public → IGW, private → NAT.
- Gateway VPC endpoints for S3 + DynamoDB (free).
- Interface VPC endpoints for Secrets Manager, ECR (API + DKR), CloudWatch Logs — the AWS APIs the CP services hit frequently. SQS deliberately not endpointed; cp-ingest's poll traffic goes through NAT.
- Five security groups: `alb` (HTTPS from anywhere), `tasks` (8080 + 3000 from `alb`), `rds` (5432 from `tasks`), `tailscale` (egress only), `vpc-endpoints` (443 from `tasks`).

`terraform fmt + validate` clean. `terraform apply` not run from this checkout — applies are deploy events.
