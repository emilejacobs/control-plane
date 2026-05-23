# Issue 01 — Terraform infra: bring Phase 0 IoT Core resources under code

Status: done

## Parent

Phase 1 roadmap: [`docs/roadmap.md`](../../../docs/roadmap.md) § Phase 1 deliverable "AWS infra in Terraform/CDK".

## Background

Phase 0 deliberately provisioned IoT Core resources manually via the runbook ([`docs/runbooks/phase-0-iot-core-provisioning.md`](../../../docs/runbooks/phase-0-iot-core-provisioning.md)) — the PRD spec says "the IoT Core CA and the one or two things for Phase 0 are provisioned manually in the console (Phase 1 codifies infra)" ([`.scratch/phase-0-agent-spike/PRD.md`](../../phase-0-agent-spike/PRD.md) line 141).

That decision held: Phase 0 closed (modulo the Linux smoke tomorrow). Now the manual provisioning friction the smoke session surfaced motivates picking up the codification work first in Phase 1, before the rest of the infra grows.

## What to build

A Terraform configuration that reproduces, in code, the resources that were provisioned manually for the Phase 0 macOS smoke:

- The shared **IoT policy** (`UknomiAgentPolicy` — see Issue 10 in Phase 0 for the corrected policy shape that actually works with `agent-cli`).
- A **per-device thing** + **mTLS cert** module, parameterised by `device_id`, so onboarding a new device is `terraform apply` with one variable.
- The **Amazon Root CA download / packaging** decision: probably a `data` source or a checked-in file referenced by config — TBD.

Out of scope for this issue (each is its own Phase 1 issue):

- VPC, ALB, RDS, Fargate cluster, S3, KMS — those land as the API service work begins.
- Tailscale subnet router task.
- A custom CA for device certs (ADR-004's install-script enrollment requires this; tracked separately).

## Decisions

These four decisions originally lived in this issue. They are PRD-level (they bind the whole infra track, not just this issue) and have moved to the Phase 1 PRD draft. Listed here for traceability:

1. **Terraform vs CDK** — settled in the PRD.
2. **State backend** — settled in the PRD.
3. **Module layout** — settled in the PRD.
4. **Cert handling** (mint via `aws_iot_certificate` vs out-of-band import) — settled in the PRD.

This issue implements against those decisions; it does not re-decide them.

## Acceptance criteria

- [x] `infra/terraform/` directory committed with provider + backend config. *(S3 + DynamoDB backend wired in `providers.tf` against `uknomi-tfstate-523612763411` / `uknomi-tfstate-locks`; one-time bucket+table bootstrap documented in the README.)*
- [x] `terraform apply` in a clean account produces the policy + a thing+cert. *(Phase-0 spike root delivered this; `provision-device.sh apply <id>` is the wrapper.)*
- [x] `terraform destroy` cleanly removes everything. *(Phase-0 cleanup ran end-to-end; documented in the README + the Wave-0 runbook.)*
- [x] A short README under `infra/terraform/` documents the workflow (`apply`/`destroy`, the device-onboarding pattern, the state-backend setup steps). *(Updated this turn: backend bootstrap + state migration section.)*
- [x] The runbook at `docs/runbooks/phase-0-iot-core-provisioning.md` gets a top-banner note: "Phase 1 codified this in `infra/terraform/`; this runbook is preserved as the manual reference." *(Banner already in place from the original codification.)*
- [x] The Phase 0 smoke is re-runnable end-to-end via `terraform apply` → install agent → smoke commands. *(The Wave-0 bench runbook from #12 walks exactly this path.)*
- [x] **Documentation updated.** *(See completion comment.)*

## Blocked by

None — independent.

## Notes from the Phase 0 smoke

- The policy as shipped in the runbook does **not** work with `agent-cli` — see [Phase 0 Issue 10](../../phase-0-agent-spike/issues/10-agent-cli-iot-policy-fix.md). Codify the corrected shape (broader `Connect`, all three thing-scoped Subscribe topic filters) rather than the broken shape.
- Account is `523612763411`, region `us-east-1`, IoT Core endpoint `agcw133a9fxn7-ats.iot.us-east-1.amazonaws.com`. Worth parameterising properly rather than hard-coding.

## Comments

### 2026-05-22 — closing literal #01; deployment infra split out to #25

Most of #01's scope landed organically with the Phase 0 codification work that ran ahead of Phase 1 (the `infra/terraform/` root with `policy.tf`, `device.tf`, `data.tf`, `outputs.tf`, `provision-device.sh`, the runbook banner) and the `bootstrap-key.tf` additions from #10. The remaining gaps closed this turn:

- `providers.tf` — the commented S3 backend placeholder is now real: bucket `uknomi-tfstate-523612763411`, DynamoDB table `uknomi-tfstate-locks`, key `iot-core/terraform.tfstate` (chosen so future roots — notably #25's deployment root — sit beside this one in the same bucket).
- `README.md` — the "see #01" pointer is gone; a new § *State backend bootstrap* spells out the one-time `aws s3api` / `aws dynamodb` CLI commands that create the bucket + table before the first `terraform init` after this change. `terraform fmt + validate` clean.
- `docs/architecture.md` — the "Pending #01" bullet became "Built (#01, #10)", and a new "Pending #25" bullet captures the rest of the Phase 1 deploy (VPC, RDS, Fargate, ALB, KMS, S3, ECR, IAM, Tailscale, IoT rules wiring, observability).

**Scope split.** When picking this up, I found a documentation drift: `architecture.md`, the Phase-0 TF README, and the Wave-0 runbook I just wrote all treated #01 as the *full Phase 1 infrastructure* root (VPC + RDS + Fargate + …). The issue text itself was much narrower — IoT Core codification, with VPC / ALB / RDS / Fargate / S3 / KMS explicitly **Out of scope** and "their own Phase 1 issue." That "own issue" did not exist in the tracker. With the user's sign-off (2026-05-22: "Split (recommended)") I honored #01 as written and filed [#25](./25-phase-1-deployment-infra.md) for the deploy root. The drifted docs are corrected this turn.

**Documentation criterion.** Discharged — `architecture.md` updated; the IoT Core TF README rewritten; #25 carries the forward-looking scope. `CONTEXT.md` unchanged (no new domain term). No ADR — the state backend choice (S3 + DynamoDB) is the Terraform-canonical pattern, not a hard-to-reverse architectural decision worth its own record beyond the README.
