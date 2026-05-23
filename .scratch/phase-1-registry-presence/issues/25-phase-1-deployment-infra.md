# Issue 25 — Phase 1 deployment infra (Terraform)

Status: ready-for-agent
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

## Decisions to confirm before HCL is written

1. **AWS account + region.** Same `523612763411` / `us-east-1` as Phase 0? If a separate deploy account is intended, surface it before SGs reference VPC IDs.
2. **DNS zone + hostnames.** Production hostname for the CP API and the dashboard. Route 53 zone already in this account, or external?
3. **Tailscale tailnet + auth-key strategy.** Reuse the existing tailnet? Ephemeral OAuth key vs reusable auth key?
4. **Image source for v0.** The Fargate task defs reference container images — do they come from the ECR repos this issue creates (in which case there is a chicken-and-egg around the first image push), or from a public placeholder until the CI pipeline (#02) is wired?

## Acceptance criteria

- [ ] New Terraform root at `infra/terraform-deploy/` (or equivalent path; final layout discussed during the structural pass), separate from the IoT Core root.
- [ ] `terraform apply` on a clean (post-bootstrap-bucket) account produces a working CP deployment: ALB returns 200/401 from `cp-api`'s health endpoint, the dashboard renders, `cp-api` connects to RDS, `cp-ingest` consumes from the SQS queues.
- [ ] A small Wave-0-bench device can be enrolled via the install module against the deployed CP, end-to-end (this is the same end-to-end exercise as #12).
- [ ] `terraform destroy` cleanly removes everything (RDS deletion-protection is the one gotcha to document).
- [ ] State backend `key` for this root is distinct from the IoT Core root's; both roots live in the same S3 bucket.
- [ ] CloudWatch alarms for the bare-minimum critical signals exist and fire correctly (verified by inducing one of each in a smoke pass).
- [ ] **Documentation updated.** `docs/architecture.md` Cloud infrastructure section reflects the deployment shape; `docs/CONTEXT.md` adds any new domain term the deploy introduces; hard-to-reverse decisions (e.g. single-region, single-NAT) are recorded as ADRs.

## Blocked by

- None structurally. Foundational — unblocks #12 (Wave 0 bench smoke), which has had this listed as a hidden prerequisite all along.

## Notes / suggested staging

The scope is large enough that a single big-bang apply is risky. A reasonable staging order, each as its own commit:

1. Networking (VPC, subnets, IGW/NAT, route tables, VPC endpoints, security groups).
2. State backend `key` for this root + the bootstrap doc.
3. KMS + Secrets Manager (DB credentials, JWT signing key placeholder, TOTP encryption key placeholder).
4. RDS Postgres (depends on networking + KMS + secrets).
5. ECR repos.
6. ECS cluster + task execution role + log groups.
7. IAM task roles per service.
8. `cp-api` task definition + service + ALB target group; ACM + Route 53 + ALB last.
9. `dashboard` task definition + service.
10. `cp-ingest` task definition + service + the `modules/sqs-ingest` instantiations + `modules/cp-ingest-service` instantiation.
11. Tailscale subnet router task + secret.
12. S3 buckets (audit mirror, command output, agent distribution).
13. CloudWatch alarms + SNS topic.
14. Docs (`architecture.md`, ADRs for the load-bearing decisions).
