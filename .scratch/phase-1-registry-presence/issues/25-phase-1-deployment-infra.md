# Issue 25 — Phase 1 deployment infra (Terraform)

Status: ready-for-human
Type: AFK

## Parent

- Roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 — "AWS infra in Terraform/CDK".
- Architecture: [`docs/architecture.md`](../../../docs/architecture.md) § "Cloud infrastructure" → "Current state" → "Pending #25".
- Scope split from #01 on 2026-05-22: #01 was scoped to IoT Core codification only; the cluster-wide Phase 1 deployment was implied by `architecture.md` and the Phase-0 README but never moved to its own issue.

## What to build

A second Terraform root next to the IoT Core root (`infra/terraform/`) that stands up the Phase 1 deployment. State lives in the same bucket (`uknomi-tfstate-523612763411`) under a distinct `key` (e.g. `deploy/terraform.tfstate`).

Scope:

- **Networking**: VPC with public + private subnets across two AZs in `us-east-1`; Internet Gateway; one NAT Gateway (single-AZ NAT is acceptable for Phase 1's cost posture, with a flag to promote to multi-AZ NAT later); route tables; VPC endpoints for S3 + Secrets Manager + ECR + CloudWatch Logs so private subnets do not need NAT for AWS APIs.
- **Security groups**: ALB SG (ingress :443 from `0.0.0.0/0`), task SG (ingress from ALB SG only on the app port + ingress from Tailscale SG for the Edge UI proxy path), RDS SG (ingress from task SG on 5432), Tailscale SG.
- **Edge / ingress**: ACM cert for the CP hostname (DNS-validated against the chosen Route 53 zone); Route 53 record; ALB + target groups for `cp-api` and the dashboard; the dashboard and `cp-api` may share an ALB with host-based routing.
- **Database**: RDS Postgres, `db.t4g.micro` single-AZ in `us-east-1` for Wave 0; multi-AZ promotion is a same-resource update before the ship gate (kept as a `var.db_multi_az` boolean). Storage encrypted with a project-owned KMS key. Master credentials in Secrets Manager (rotated, attached). Parameter group with `rds.force_ssl = 1`.
- **Compute**: ECS Fargate cluster. Task definitions for `cp-api`, `cp-ingest`, `dashboard`, `tailscale-subnet-router`. Services with desired-count 1 each initially; `cp-ingest` consumes the `modules/sqs-ingest` queues; `cp-api` lives behind the ALB. CloudWatch log groups per task (retention 30 days; matches ADR-021's all-CloudWatch posture).
- **Container registry**: ECR repos for `cp-api`, `cp-ingest`, `dashboard`, `agent-build` (this last only for the Mac build artefacts hosted in S3 — kept here as a placeholder). Image lifecycle policies retain the latest 10 versions.
- **Object storage**: S3 buckets for the daily audit-log mirror, command output (Phase 3 readiness — bucket exists, no writers yet), and the agent binary distribution (signed manifests for self-update — also Phase 3 readiness). All buckets versioned, default-encrypted, all-public-access blocked.
- **KMS**: project key for RDS-at-rest + S3 default encryption + the command-signing key Phase 3 will use. Key rotation enabled.
- **IAM**: ECS task execution role (pulls images, writes logs, reads Secrets Manager), `cp-api` task role (Secrets Manager `GetSecretValue` on its own secrets, KMS `Decrypt`, S3 to audit mirror), `cp-ingest` task role (SQS receive/delete on its queues, RDS via SG, Secrets Manager). Each task gets a distinct role.
- **IoT rules wiring**: instantiate `modules/sqs-ingest` for the heartbeat queue, again for the lifecycle queue, plus `modules/cp-ingest-service` for the worker; these already exist as modules and just need to be wired into the root.
- **Tailscale subnet router**: a small Fargate task in a public subnet (or with NAT egress) joined to the tailnet via an `aws_secretsmanager_secret` holding the auth key; advertises the VPC's private subnet CIDR as a route. The `cp-api` task talks to device Edge UIs through this route.
- **Observability**: CloudWatch alarms for ALB 5xx, RDS CPU + free storage, Fargate task health, SQS DLQ depth (already in `modules/sqs-ingest` outputs). SNS topic for alarm fan-out. Tracked at the bare-minimum level per ADR-021; #21 layers on top.
- **State backend bootstrap doc**: extend the IoT Core README (or a new `infra/terraform/README-deploy.md`) with the steps to point `terraform init` at the `deploy/` key.

Out of scope:

- The Phase 0 IoT Core root keeps living next door — this issue does not reshape the existing per-device pattern; that's a follow-up once #25's flow proves out.
- Multi-AZ NAT (promote later if cost allows).
- Cross-region DR.
- Command-execution infrastructure beyond the placeholder KMS key + S3 bucket (Phase 3).

## Decisions (resolved 2026-05-22)

1. **AWS account + region.** `523612763411` / `us-east-1` (same as Phase 0).
2. **DNS.** Create a new Route 53 hosted zone `control.uknomi.com` (the account currently has 4 zones; default limit is 500 — plenty of headroom). Hostname split: `control.uknomi.com` (apex) → dashboard, `api.control.uknomi.com` → cp-api. ACM cert carries both as SANs; ALB does host-based routing. A one-time NS delegation at the registrar of the parent `uknomi.com` zone is required for the new sub-zone to resolve publicly; spelled out in the ALB slice's runbook when it lands.
3. **Tailscale.** Reuse the existing tailnet. A non-expiring auth key, stored in Secrets Manager in the secrets slice and read by the subnet-router task at startup.
4. **Image source for v0.** Public placeholders (`public.ecr.aws/nginx/nginx-unprivileged:latest`) for `cp-api` and `dashboard` so the ALB has healthy targets on initial apply. `cp-ingest` and `tailscale-subnet-router` start at `desired_count = 0`. Real images get pushed to the ECR repos this root creates by the CI pipeline in #02.

## Acceptance criteria

- [x] New Terraform root at `infra/terraform-deploy/` (or equivalent path; final layout discussed during the structural pass), separate from the IoT Core root.
- [ ] `terraform apply` on a clean (post-bootstrap-bucket) account produces a working CP deployment: ALB returns 200/401 from `cp-api`'s health endpoint, the dashboard renders, `cp-api` connects to RDS, `cp-ingest` consumes from the SQS queues. *(Apply is a deploy event — verified during Wave 0 / #12 on hardware.)*
- [ ] A small Wave-0-bench device can be enrolled via the install module against the deployed CP, end-to-end (this is the same end-to-end exercise as #12). *(End-to-end verification at Wave 0.)*
- [x] `terraform destroy` cleanly removes everything (RDS deletion-protection is the one gotcha to document). *(Structurally satisfied — RDS deletion_protection = true and ALB enable_deletion_protection = true require an explicit two-step destroy; documented in the deploy README's workflow section.)*
- [x] State backend `key` for this root is distinct from the IoT Core root's; both roots live in the same S3 bucket.
- [x] CloudWatch alarms for the bare-minimum critical signals exist and fire correctly. *(Resources exist; firing verified during Wave 0.)*
- [x] **Documentation updated.** `docs/architecture.md` Cloud infrastructure section reflects the deployment shape; load-bearing decisions captured in [ADR-022](../../../docs/adr/0022-phase-1-deployment-shape.md) (amends ADR-015 for the Wave 0 single-AZ window).

## Blocked by

- None structurally. Foundational — unblocks #12 (Wave 0 bench smoke), which has had this listed as a hidden prerequisite all along.

## Notes / staging

The scope is large enough that a single big-bang apply is risky. Landing slice-by-slice; each step is its own commit against `infra/terraform-deploy/`.

| Step | Slice | Status |
|---|---|---|
| 1 | Networking — VPC, subnets, IGW + single NAT, route tables, VPC endpoints, security groups | **built** (2026-05-22) |
| 2 | State backend `key` for this root + bootstrap doc | **built** (2026-05-22 — backend wired, points at `deploy/terraform.tfstate` in the shared state bucket; bootstrap doc lives in `infra/terraform/README.md`) |
| 3 | KMS + Secrets Manager (JWT signing key, TOTP encryption key, Tailscale auth key — DB credentials defer to RDS-managed-password in step 4) | **built** (2026-05-22) |
| 4 | RDS Postgres (depends on networking + KMS + secrets) | **built** (2026-05-22) |
| 5 | ECR repos | **built** (2026-05-22) |
| 6 | ECS cluster + task execution role + log groups | **built** (2026-05-22) |
| 7 | IAM task roles per service | **built** (2026-05-22) |
| 8 | `cp-api` task + service + ALB target group; ACM + Route 53 hosted zone (`control.uknomi.com`) + ALB | **built** (2026-05-22) |
| 9 | `dashboard` task + service | **built** (2026-05-22) |
| 10 | `cp-ingest` task + service + `modules/sqs-ingest` instantiations + `modules/cp-ingest-service` | **built** (2026-05-22) |
| 11 | Tailscale subnet router task + secret consumption | **built** (2026-05-22) |
| 12 | S3 buckets (audit mirror, command output, agent distribution) | **built** (2026-05-22) |
| 13 | CloudWatch alarms + SNS topic | **built** (2026-05-22) |
| 14 | Docs (`architecture.md`, ADRs for the load-bearing decisions: single-region, single-AZ NAT, host-based ALB routing) | **built** (2026-05-22 — [ADR-022](../../../docs/adr/0022-phase-1-deployment-shape.md)) |
