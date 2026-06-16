# Terraform — IoT Core root (Phase 0 + #01 + #10)

This root codifies the IoT Core resources for the Phase 0 spike and the bootstrap-key plumbing from #10: one shared policy, one thing+cert per device (parameterised by `device_id`), and the `uknomi/cp/bootstrap-key` Secrets Manager secret. (The CI role that reads that secret lives in `terraform-deploy/edge-install-ci.tf`, #90 — kept out of this per-device root so it doesn't require a `device_id`.)

State lives on S3 with DynamoDB locking — see [§ State backend bootstrap](#state-backend-bootstrap) for the one-time setup.

The **Phase 1 deployment infra** — VPC, ALB, RDS Postgres, Fargate cluster, KMS, S3, ECR, IAM, Tailscale subnet router, IoT rules wiring, CloudWatch — is a separate Terraform root tracked in [issue #25](../../.scratch/phase-1-registry-presence/issues/25-phase-1-deployment-infra.md). It lives next to this root in the same state bucket (different `key`), not in this root.

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

State lives on S3 with DynamoDB locking — configured in `providers.tf` against:

- **Bucket**: `uknomi-tfstate-523612763411` (versioning + AES256 SSE + all-public-access blocked)
- **Lock table**: `uknomi-tfstate-locks` (PAY_PER_REQUEST, partition key `LockID`)
- **Key for this root**: `iot-core/terraform.tfstate`

The state still contains private key material (`aws_iot_certificate` returns the PEM); the bucket-level encryption + IAM scoping is the protection. Treat any download of state files as a secret event.

### State backend bootstrap

One-time, run by an admin role in the target AWS account *before* the first `terraform init` of this root:

```bash
ACCOUNT_ID=523612763411
REGION=us-east-1
BUCKET=uknomi-tfstate-${ACCOUNT_ID}

# State bucket — versioned, encrypted, no public access.
aws s3api create-bucket --bucket "$BUCKET" --region "$REGION"
aws s3api put-bucket-versioning --bucket "$BUCKET" \
  --versioning-configuration Status=Enabled
aws s3api put-bucket-encryption --bucket "$BUCKET" \
  --server-side-encryption-configuration \
  '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
aws s3api put-public-access-block --bucket "$BUCKET" \
  --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

# Lock table — pay-per-request so it costs nothing when idle.
aws dynamodb create-table \
  --table-name uknomi-tfstate-locks \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region "$REGION"
```

If you already have local state from a Phase 0 apply, `terraform init` will offer to migrate it on the first run after this backend block lands. Answer **yes** to migrate; the local file becomes redundant.

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

- VPC, ALB, RDS, Fargate, KMS, S3 buckets, ECR, IAM roles for tasks, Tailscale subnet router, IoT rules wiring, CloudWatch alarms — the Phase 1 deployment-infra root, tracked in [issue #25](../../.scratch/phase-1-registry-presence/issues/25-phase-1-deployment-infra.md).
- The shared `UknomiAgentPolicy` does not live in its own module yet — that and the device-as-module split are reshape candidates when #25's root lands and absorbs or supersedes this one.
- No custom CA. The cert is `aws_iot_certificate` (AWS-managed mint). ADR-004's install-script enrollment with a uKnomi-owned CA is a Phase 4 concern.
- No CloudWatch logging config for IoT Core (deferred to #25).

## Known gotcha: agent-cli ↔ policy

The policy here uses the **corrected** shape, not the one the IoT provisioning runbook currently ships. See [Phase 0 issue 10](../../.scratch/phase-0-agent-spike/issues/10-agent-cli-iot-policy-fix.md) for why the runbook's policy denies `agent-cli`. The runbook still needs to be updated to match.
