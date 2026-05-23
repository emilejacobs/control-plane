# ADR-022: Phase 1 deployment-infra shape

**Status:** Accepted (2026-05-22)

**Amends:** [ADR-015](./0015-postgres-multi-az.md) (RDS multi-AZ posture for Wave 0 only)

**Context.** Issue #25 stands up the Phase 1 deployment infrastructure in Terraform: VPC, RDS Postgres, Fargate (cp-api, cp-ingest, dashboard, Tailscale subnet router), ALB + ACM + Route 53, KMS, S3, ECR, IAM, IoT rules wiring, CloudWatch alarms. Several shape decisions are load-bearing in the "hard to revert without state migration" sense; this ADR records them once rather than scattering rationale across HCL comments.

## Decisions

### 1. Two Terraform roots in one state bucket

`infra/terraform/` (the IoT Core root from #01) and `infra/terraform-deploy/` (the deployment root from #25) live side-by-side as **sibling roots**, not nested modules. Both store state in the same S3 bucket (`uknomi-tfstate-523612763411`) under distinct keys (`iot-core/terraform.tfstate`, `deploy/terraform.tfstate`) with the same DynamoDB lock table.

Why two roots:

- The IoT Core root is per-device (the `provision-device.sh` flow from Phase 0 + the bootstrap-key resources from #10). Folding it into the deploy root would mix per-device state with cluster-wide state — bad blast radius if a device-level `terraform destroy` ever runs.
- Plan/apply scope. The deploy root is large; the IoT Core root is small. Splitting them lets routine device provisioning happen without re-planning the entire CP.
- Each root can be re-platformed independently if needed (e.g. the IoT Core root is a natural candidate for absorption into the deploy root once the per-device pattern is reshaped, but that's a separate refactor).

Why one bucket: one place to back up, one IAM policy to scope, one lock table. The `key` distinction is enough isolation.

The cost: cross-root references go through `terraform_remote_state` or hardcoded ARNs/names. In practice the only cross-root coupling today is the IoT policy name (`UknomiAgentPolicy`) and the bootstrap-key Secrets Manager secret (`uknomi/cp/bootstrap-key`), both referenced by name rather than ARN — so no `terraform_remote_state` dependency. Acceptable.

### 2. RDS single-AZ for Wave 0, multi-AZ before the ship gate

This **amends ADR-015** ("Postgres multi-AZ from day one"). ADR-015's reasoning still holds for the ship gate; it does not hold for Wave 0, which is a single-device bench smoke that does not need RDS HA. Single-AZ saves ~$15/month during the Wave 0 → Wave 1 staging window.

Implementation: `var.db_multi_az` defaults to `false`. The Wave-0 → Wave-1 transition flips it to `true` and `terraform apply` performs an in-place conversion (a one-shot ~10-minute window of restricted writes; documented as part of the Wave-1 runbook when it lands).

Reversibility: trivial — flip the variable. The ADR-015 ship-gate posture is honored by the time the ship gate is verified.

### 3. Single NAT gateway

One NAT Gateway in `us-east-1a`, not per-AZ. Phase 1's egress traffic is small (cp-ingest poll loops + occasional ECR pulls + Secrets Manager + CloudWatch); a single NAT meets it. A NAT-AZ outage degrades but does not break the CP: agents reconnect via IoT Core (no NAT involved); cp-api loses egress for AWS APIs not covered by the VPC endpoints, which is recoverable in minutes by promoting NAT to per-AZ.

The promotion is a structural change (additional `aws_nat_gateway` + route table) rather than a variable flip. Acceptable cost for Phase 1; revisit if traffic patterns demand it.

### 4. Host-based ALB routing with one Route 53 zone

One shared ALB serves both surfaces. One Route 53 hosted zone (`control.uknomi.com`) holds both records: `control.uknomi.com` (apex) → dashboard, `api.control.uknomi.com` → cp-api. One ACM cert covers both as SANs.

Why one zone: simplest delegation story (one NS handoff at the registrar of `uknomi.com`). Why two records vs path-based on one host: cp-api and the dashboard both serve `/devices` — different methods + payloads, but the same path — so they have to be on different hostnames.

Reversibility: easy — adding path-based listener rules over time does not require structural changes.

### 5. Customer-managed KMS key, scoped via `kms:ViaService`

One customer-managed KMS key (`alias/uknomi-cp`) is the at-rest encryption key for RDS, Secrets Manager, and Performance Insights. ECR and S3 (in this Phase 1 slice) deliberately use AWS-managed keys to avoid the cross-cutting key-policy churn for not-customer-data assets; later hardening passes may flip them.

Each service principal in the key policy is gated by `kms:ViaService` so a stolen IAM token cannot use the key for anything except the intended service path. IAM identity delegation goes through the standard root-permits-IAM pattern.

## Consequences

- (+) Plan/apply scopes match the natural blast radius of each layer (device-level vs cluster-level).
- (+) Cost during the Wave 0 → ship gate window is bounded; multi-AZ premium only paid when uptime SLA is verified.
- (+) The DNS shape is simple: one zone, one cert, two records.
- (-) Two Terraform roots means two `init`s, two state files to back up, two places to grep for resources. Documented in `infra/terraform-deploy/README.md` and `infra/terraform/README.md`.
- (-) Single NAT means an AZ-specific outage briefly degrades cp-api's AWS-API egress. Acceptable; promotion to per-AZ is a known-cost upgrade.
- (-) Adding a new service principal to the KMS key policy requires a key-policy update (Terraform handles this in-place but it's a small re-apply event each time a slice needs the key).

## Verification

`terraform validate` clean on both roots from `da6a64f` onwards in `infra/terraform/` and from `d88796e` through `6a7d9fd` in `infra/terraform-deploy/`. `terraform apply` for the deploy root is a deploy event, not a CI gate; the runbook at `docs/runbooks/phase-1-wave-0-bench.md` is what actually exercises it end-to-end.
