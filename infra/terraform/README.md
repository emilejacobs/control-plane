# Terraform — Phase 0 IoT Core resources

Minimal starter that codifies the IoT Core resources the Phase 0 spike needs (one shared policy + one thing/cert per device). Designed for one-command device provisioning during the macOS and Linux smoke runs.

This is a starter — the full Phase 1 Terraform scope (VPC, ALB, RDS, Fargate, KMS, S3, Tailscale subnet router) lands in [Phase 1 issue 01](../../.scratch/phase-1-registry-presence/issues/01-terraform-iot-core.md), which also decides the S3 + DynamoDB state backend, the device-as-module split, and the controller-cert separation.

## Workflow — provision a device

```bash
cd infra/terraform
./provision-device.sh apply dev-pi-emile
```

That:
1. `terraform init` (idempotent)
2. `terraform apply` to create `UknomiAgentPolicy` (or no-op if it exists), a thing named `dev-pi-emile`, a fresh cert+key, and the attachments
3. Writes `out/dev-pi-emile/{cert.pem,private.key,ca.pem,agent.json}` ready to `scp` to the device

Then follow [`docs/runbooks/phase-0-agent-install.md`](../../docs/runbooks/phase-0-agent-install.md) to scp + install on the target device (paths match the runbook).

## Workflow — tear down

```bash
./provision-device.sh destroy dev-pi-emile
```

Removes the thing, cert, attachments, **and** the shared policy. If you have other devices provisioned via this config, do them last — destroying any one device removes the shared `UknomiAgentPolicy` (single-device-per-state design; see "Multi-device" below).

## State

Local state, deliberately. `terraform.tfstate` lives in this directory and is gitignored. It contains the private key for the cert — protect it like any other secret.

For multi-developer use, move to:
- S3 bucket with default encryption + bucket policy scoping access
- DynamoDB table for state locking

That's a Phase 1 issue 01 deliverable, not Phase 0 spike work.

## Multi-device

This config provisions **one device per state**. To provision both a Mac and a Pi simultaneously, use Terraform workspaces:

```bash
terraform workspace new pi-smoke
./provision-device.sh apply dev-pi-emile

terraform workspace new mac-smoke
./provision-device.sh apply dev-mac-mini-emile
```

Each workspace gets its own state file under `terraform.tfstate.d/`. The shared policy is recreated per workspace — that's redundant but harmless; AWS IoT policies are name-scoped, so the second workspace's apply will fail with `ResourceAlreadyExistsException` if a policy of the same name already exists from another workspace. For the Phase 0 spike, just do one device at a time and destroy between runs. The full multi-device model (data-source lookup of the shared policy, separate device modules) is the Phase 1 follow-up.

## What's deliberately NOT here

- The shared `UknomiAgentPolicy` does not live in its own `infra/terraform/shared/` module yet — that's the cleaner shape for Phase 1.
- No custom CA. The cert is `aws_iot_certificate` (AWS-managed mint). ADR-004's install-script enrollment with a uKnomi-owned CA is a Phase 1 deliverable.
- No remote state, no IAM scoping of who can apply.
- No CloudWatch logging config for IoT Core (deferred).

## Known gotcha: agent-cli ↔ policy

The policy here uses the **corrected** shape, not the one the IoT provisioning runbook currently ships. See [Phase 0 issue 10](../../.scratch/phase-0-agent-spike/issues/10-agent-cli-iot-policy-fix.md) for why the runbook's policy denies `agent-cli`. The runbook still needs to be updated to match.
