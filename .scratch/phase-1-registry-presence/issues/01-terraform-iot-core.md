# Issue 01 — Terraform infra: bring Phase 0 IoT Core resources under code

Status: ready-for-agent

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

## Decisions to make in this issue

1. **Terraform vs CDK.** Roadmap says "Terraform/CDK"; pick one and document the choice. Recommendation: Terraform — the AWS provider has full coverage, HCL is easier to review than TypeScript-CDK output, and the team has no existing CDK assets.
2. **State backend.** Local state is fine to start, but moves to S3 + DynamoDB lock table before any second person touches it. Either way, decide and document.
3. **Module layout.** Suggested:
   ```
   infra/terraform/
     providers.tf
     backend.tf
     iot/
       policy.tf
       README.md
     modules/
       device/
         main.tf
         variables.tf
         outputs.tf      # cert PEM, key PEM, thing name
     devices/
       dev-mac-mini-emile.tf   # (or similar — one .tf per provisioned device)
   ```
4. **Cert handling.** `aws_iot_certificate` with `active = true` mints the cert; the private key comes back in state. State must be encrypted at rest (S3 default encryption + IAM scoped on the bucket). Alternative: out-of-band key generation + import via `aws_iot_certificate.certificate_pem` — more secure but more workflow friction.

## Acceptance criteria

- [ ] `infra/terraform/` directory committed with provider + backend config.
- [ ] `terraform apply` in a clean account produces:
  - One `UknomiAgentPolicy` matching the corrected shape from Phase 0 Issue 10.
  - One thing `dev-mac-mini-emile` (or whatever name the device-Terraform variable resolves to) with a fresh cert attached.
- [ ] `terraform destroy` cleanly removes everything (matches the manual cleanup we ran at the end of Phase 0).
- [ ] A short README under `infra/terraform/` documents the workflow (`apply`/`destroy`, the device-onboarding pattern, the state-backend setup steps).
- [ ] The runbook at `docs/runbooks/phase-0-iot-core-provisioning.md` gets a top-banner note: "Phase 1 codified this in `infra/terraform/`; this runbook is preserved as the manual reference."
- [ ] The Phase 0 smoke is re-runnable end-to-end via `terraform apply` → install agent → smoke commands (no manual `aws iot ...` calls beyond what Terraform issues).

## Blocked by

None — independent.

## Notes from the Phase 0 smoke

- The policy as shipped in the runbook does **not** work with `agent-cli` — see [Phase 0 Issue 10](../../phase-0-agent-spike/issues/10-agent-cli-iot-policy-fix.md). Codify the corrected shape (broader `Connect`, all three thing-scoped Subscribe topic filters) rather than the broken shape.
- Account is `523612763411`, region `us-east-1`, IoT Core endpoint `agcw133a9fxn7-ats.iot.us-east-1.amazonaws.com`. Worth parameterising properly rather than hard-coding.
