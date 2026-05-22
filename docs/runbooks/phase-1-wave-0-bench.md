# Phase 1 — Wave 0 (Bench) end-to-end smoke

> **HITL.** This runbook is read top-to-bottom by an engineer at the bench
> Mac with AWS credentials, an open terminal, and the deployed dashboard in a
> browser. It is the verification of [Phase 1 issue #12](../../.scratch/phase-1-registry-presence/issues/12-wave-0-bench-smoke.md).
> Treat surprises as *defects to file as new issues*, not as things to
> fix-as-you-go.

## What this runbook does

Take the same Mac that was the Phase 0 dev device (`dev-mac-mini-emile`),
decommission its Phase 0 thing + cert, re-provision it through the Phase 1
install module against the deployed CP, and verify the full stack end-to-end
on this one device before any client site is touched.

Exit criteria — every box ticked under [§ Smoke checklist](#5-smoke-checklist),
and the [§ 30-minute monitoring window](#6-30-minute-monitoring-window)
finishes with no DLQ messages and no alarms.

## Prerequisites

Listed here so a second engineer can confirm at a glance what is in place
before they start. **Status as of 2026-05-22:**

| # | Prerequisite | Status |
|---|---|---|
| #01 | Phase 1 Terraform — VPC, ALB, RDS, Fargate, deployed `cp-api`, `cp-ingest`, dashboard, Tailscale subnet router | **pending** — Wave 0 can only run once #01 lands |
| #10 | `uknomi/cp/bootstrap-key` Secrets Manager secret applied **and** populated with the real key (`aws secretsmanager put-secret-value`) | secret HCL landed; placeholder; real key not yet set |
| #10 / mac-mini-rollout CI | The install package's `secrets/cp-bootstrap-key` written by CI at build time | **deferred** — tracked in `mac-mini-rollout` (see [#10's completion comment](../../.scratch/phase-1-registry-presence/issues/10-bootstrap-key-secrets-manager.md)) |
| #11 | `mac-mini-rollout/modules/11-cp-agent.sh` exists | landed (`6a2aeed` in the rollout repo) |
| #17 / #18 | Dashboard `/devices` and `/devices/[id]` views | landed |
| #19 | Structured logs + correlation IDs (`cplog`) | landed |

If any *pending* row blocks you, file the gap as a new issue rather than
papering over it.

Local prerequisites at the bench:

- The bench Mac powered on, on Wi-Fi the operator can reach the deployed CP
  from.
- AWS CLI v2 configured with credentials that can touch IoT Core in
  `us-east-1` and write to CloudWatch (`aws configure`).
- A terminal where the rest of this runbook runs as the bench Mac user; some
  steps will `sudo`.
- The deployed dashboard URL — write it down up front:
  - `CP_BASE_URL=https://cp.uknomi.example`
- The IoT Core ATS endpoint:
  - `CP_BROKER_URL=tls://agcw133a9fxn7-ats.iot.us-east-1.amazonaws.com:8883`

```bash
export AWS_REGION=us-east-1
export PHASE0_DEVICE_ID=dev-mac-mini-emile
export WAVE0_HOSTNAME=mac-mini-bench-01   # matches the CP naming-convention regex
```

> Naming note. `mac-mini-bench-01` is chosen so it matches the CP's
> hostname-convention regex `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$` and does
> **not** raise the `audit.enrollment.anomaly` alert. The
> `mac-mini-rollout` repo's default hostname schemes do not match this
> regex — see finding #2 on [issue #11](../../.scratch/phase-1-registry-presence/issues/11-install-module-cp-agent.md);
> Wave 0 uses the bench-specific hostname so the alert log stays clean during
> the smoke window.

## 1. Decommission the Phase 0 device

The Phase 0 thing + cert + local agent get removed before re-provisioning.
This is one-time work — the bench Mac never has a Phase 0 enrolment after
this section runs.

### 1a. Remove the local agent

On the bench Mac:

```bash
sudo launchctl bootout system/com.uknomi.agent 2>/dev/null || \
  sudo launchctl unload /Library/LaunchDaemons/com.uknomi.agent.plist 2>/dev/null || true
sudo rm -f /Library/LaunchDaemons/com.uknomi.agent.plist
sudo rm -f /usr/local/bin/uknomi-agent
sudo rm -rf /etc/uknomi /var/uknomi
```

Verify:

```bash
launchctl list | grep com.uknomi || echo "no uknomi LaunchDaemon — good"
test ! -e /usr/local/bin/uknomi-agent && echo "agent binary removed"
```

### 1b. Revoke and delete the Phase 0 cert + thing

The Phase 0 thing was provisioned by `infra/terraform/provision-device.sh
apply dev-mac-mini-emile`. Tear it down via the same script when its
Terraform state is still intact:

```bash
cd "${REPO_ROOT}/infra/terraform"
./provision-device.sh destroy "${PHASE0_DEVICE_ID}"
```

If the state has been lost (or `terraform destroy` reports drift / "not in
state"), fall back to manual AWS CLI:

```bash
# Find the cert ARN(s) attached to the thing.
aws iot list-thing-principals --thing-name "${PHASE0_DEVICE_ID}"

# For each principal cert ARN:
CERT_ID="$(basename <cert-arn>)"
aws iot detach-thing-principal --thing-name "${PHASE0_DEVICE_ID}" \
  --principal "<cert-arn>"
aws iot update-certificate --certificate-id "${CERT_ID}" \
  --new-status REVOKED
aws iot list-attached-policies --target "<cert-arn>" \
  --query 'policies[].policyName' --output text | \
  xargs -n1 -I{} aws iot detach-policy --policy-name {} --target "<cert-arn>"
aws iot delete-certificate --certificate-id "${CERT_ID}"

aws iot delete-thing --thing-name "${PHASE0_DEVICE_ID}"
```

> The `provision-device.sh destroy` path also removes the shared
> `UknomiAgentPolicy`. **Do not destroy the policy** — re-create it via the
> next step if the script removed it, otherwise CP-side enrolment fails when
> it tries to attach the policy to the cert it mints.

### 1c. Verify the device is gone from AWS

```bash
aws iot describe-thing --thing-name "${PHASE0_DEVICE_ID}" 2>&1 | \
  grep -q ResourceNotFoundException && echo "Phase 0 thing decommissioned"
```

## 2. Apply Phase 1 IoT Core infrastructure

Phase 1's Terraform root (issue #01) provisions: the shared
`UknomiAgentPolicy`, the `uknomi/cp/bootstrap-key` Secrets Manager secret +
CI IAM role, and the rest of the Phase 1 stack (VPC, ALB, RDS, Fargate, etc.).
Until #01 lands, the Phase 0 root in `infra/terraform/` carries only the
policy + the bootstrap-key secret resources from #10; it is **not** a full
Phase 1 deployment.

```bash
cd "${REPO_ROOT}/infra/terraform"
terraform init
terraform plan
terraform apply
```

> **Stop here if #01 has not landed.** A Wave 0 smoke against an
> incompletely-deployed Phase 1 is testing the wrong thing. File the gap
> ("Wave 0 needs the Phase 1 CP deployed via #01") as the only outcome of
> this attempt.

Set the real bootstrap key (one-time, out-of-band — Terraform seeds a
placeholder):

```bash
# Generate or fetch the real key, then:
aws secretsmanager put-secret-value \
  --secret-id uknomi/cp/bootstrap-key \
  --secret-string "$(openssl rand -base64 48)"
# Note: rebuild and redistribute the install package after a rotation.
```

Confirm the CP can read it (cp-api logs `bootstrap key loaded` at startup —
see the Fargate log group).

## 3. Build the install package with the baked bootstrap key

The `mac-mini-rollout` CI (issue #11 AC2, deferred to that repo) is what bakes
`secrets/cp-bootstrap-key` into the install package at build time. Until that
workflow lands, do it by hand on the build machine:

```bash
cd "${ROLLOUT_REPO}"
mkdir -p secrets
aws secretsmanager get-secret-value \
  --secret-id uknomi/cp/bootstrap-key \
  --query SecretString --output text > secrets/cp-bootstrap-key
chmod 600 secrets/cp-bootstrap-key
test -s secrets/cp-bootstrap-key && echo "bootstrap key baked"
```

Also bake the agent binary + version:

```bash
# From the control-plane repo:
GOOS=darwin GOARCH=arm64 go build -o "${ROLLOUT_REPO}/bin/uknomi-agent" ./cmd/agent
git -C "${REPO_ROOT}" rev-parse --short HEAD > "${ROLLOUT_REPO}/bin/uknomi-agent.version"
```

Optional: bundle the AWS IoT root CA so the module does not have to download
it at install time:

```bash
mkdir -p "${ROLLOUT_REPO}/certs"
curl -sS -o "${ROLLOUT_REPO}/certs/AmazonRootCA1.pem" \
  https://www.amazontrust.com/repository/AmazonRootCA1.pem
```

## 4. Run the install module on the bench Mac

Stage `mac-mini-rollout` onto the bench Mac (scp, USB, or `git clone` if the
bench can reach the rollout repo). On the bench Mac:

```bash
cd ~/mac-mini-rollout
cp .env.example .env
# Edit .env: set CP_BASE_URL, CP_BROKER_URL, TAILSCALE_AUTH_KEY, AWS creds.
# Leave CP_BOOTSTRAP_KEY unset — the baked secrets/cp-bootstrap-key wins.

# Set the bench hostname so it matches the CP naming convention.
sudo scutil --set HostName       "${WAVE0_HOSTNAME}"
sudo scutil --set LocalHostName  "${WAVE0_HOSTNAME}"
sudo scutil --set ComputerName   "${WAVE0_HOSTNAME}"

# Phase 1 module set, fresh start.
sudo ./setup.sh --phase 1
```

Watch for the lines the module emits — the success path is:

```
[INFO] Enrolling mac-mini-bench-01 (hardware <UUID>) against https://cp...
[INFO] Calling POST https://cp.../enrollments
[OK]   Device enrolled (HTTP 201)
[OK]   Installed device cert + key for <device_id>
[OK]   Installed uknomi-agent to /usr/local/bin/uknomi-agent
[OK]   LaunchDaemon com.uknomi.agent loaded
[INFO] Waiting for the agent to connect to IoT Core...
[OK]   uknomi-agent connected to IoT Core (device <device_id>)
```

If any line is missing or `setup.sh` exits non-zero, **stop and file**.

Capture the device id assigned by the CP — needed for the smoke checks:

```bash
DEVICE_ID="$(python3 -c 'import json; print(json.load(open("/var/uknomi/agent-config.json"))["device_id"])')"
echo "$DEVICE_ID"
```

## 5. Smoke checklist

Run each item in order, on the bench Mac (or against the deployed CP from
the operator's laptop). Tick the boxes in the issue as each passes; on a
failure, file a new issue with the exact command + output and stop.

### 5a. Device appears in `GET /devices`

In the dashboard at `${CP_BASE_URL}/devices`, the new device's hostname
(`mac-mini-bench-01`) is in the fleet list under "Unassigned" (no site
binding yet — Phase 1 has no site-assignment UI). The presence chip is
green. Click through to `/devices/{device_id}` — every field from PRD User
Story 20 is populated.

Or via the API directly (needs a JWT — see § 5c for login):

```bash
curl -sS -H "Authorization: Bearer ${JWT}" "${CP_BASE_URL}/devices" | \
  python3 -m json.tool | grep -A1 "$DEVICE_ID"
```

### 5b. Presence transitions

Three perturbations, each must move the presence chip to offline within the
expected window and back to online within ~10s (one fleet-view poll) of
recovery:

1. **Agent restart** — the cleanest case. Presence may briefly drop and
   should recover within one poll cycle (~10s).
   ```bash
   sudo launchctl kickstart -k system/com.uknomi.agent
   ```
2. **Network drop** — pull Wi-Fi or `sudo ifconfig en0 down` for ≥30 s. IoT
   Core's lifecycle disconnect should fire; the dashboard chip flips offline
   within seconds (fast-path), well under the 90 s sweeper cap. Bring the
   network back; the chip flips online within ~10 s.
3. **Power yank** — hold the power button (the brutal case). No lifecycle
   event fires because there is no TCP FIN; the dashboard must flip offline
   within ~90 s on the sweeper's next tick. Power back on; the agent
   restarts (LaunchDaemon `KeepAlive`) and the chip flips online within ~10s
   after the first heartbeat.

Document the observed timing for each in the smoke notes.

### 5c. Login + TOTP works

If this is the first time anyone has logged in:

1. Open the dashboard. If the database has zero operators, the first-run
   admin flow at `/auth/first-run` will be the landing page; otherwise log
   in as the existing operator.
2. Complete TOTP enrolment if forced (the QR + recovery codes flow from
   #05).
3. Land on `/devices` and confirm the bench Mac is visible. Note the JWT in
   the network tab if you need it for § 5a's direct API call.

### 5d. Cert expiry surfaces on the per-device view

On `/devices/{device_id}`:

- The "Certificate expires" line shows a date ~365 days out.
- The days-remaining number is color-coded **green** (the cert is fresh —
  green is `>180` per #09's thresholds).

If the cert was minted with an unexpected TTL or no date shows, file a
defect — do not adjust thresholds to make the check pass.

### 5e. Audit log captures enrolment and restart

The audit log is structured log lines in the cp-api log stream (the
`audit_log` table itself is issue #20). Open CloudWatch Logs for the cp-api
log group and filter:

```text
"msg":"audit.enrollment"
"msg":"audit.login"
```

Confirm at least one `audit.enrollment` line with `outcome=success` and
`hardware_uuid=<the bench Mac's UUID>` and `source_ip=<the bench Mac's
public IP>`. Confirm at least one `audit.login` line for the operator from
§ 5c.

Then trigger a manual restart and confirm the agent reconnect is logged in
`cp-ingest` (lifecycle event):

```bash
sudo launchctl kickstart -k system/com.uknomi.agent
```

Look for the IoT lifecycle event being consumed by `cp-ingest` and an
`is_online` flip in the `cp-ingest` log stream.

### 5f. No DLQ messages, no alarms fire

Open the CloudWatch console and check the SQS DLQs created by #07 / #08
(`presence-heartbeat-dlq`, `presence-lifecycle-dlq`) — both must show
`ApproximateNumberOfMessages` of `0` throughout the smoke. CloudWatch alarms
(#21, if landed) must remain in `OK`. If any DLQ is non-empty or any alarm
fires, file a defect with the message body and stop.

## 6. 30-minute monitoring window

Leave the bench Mac idle, logged in to the dashboard, with CloudWatch open
for ~30 minutes after § 5 completes. The exit conditions:

- Presence chip remains green for the whole window.
- `last_seen` ago-string climbs by ~1 s/s without flipping past the 90 s
  offline threshold.
- No DLQ messages accumulate; no alarms.
- No unexpected `audit.enrollment.anomaly` lines in the log stream.

Note the start and end timestamps and the observed counts — this is the
record that Wave 0 actually held.

## 7. Rollback / cleanup

If anything in § 5 / § 6 fails badly enough to abandon the smoke:

```bash
# On the bench Mac:
cd ~/mac-mini-rollout
sudo ./uninstall-cp-agent.sh
```

Then revoke the cert + delete the thing via the manual `aws iot` flow
(§ 1b) — there is no Phase 1 CP revocation endpoint (that lands in
Phase 3). Reset the bench hostname:

```bash
sudo scutil --set HostName       "dev-mac-mini-emile"
sudo scutil --set LocalHostName  "dev-mac-mini-emile"
sudo scutil --set ComputerName   "dev-mac-mini-emile"
```

The bench Mac is now in a clean pre-Wave-0 state. File the defects that
caused the abort, fix them in their own issues, then re-run this runbook
top-to-bottom.

## What this runbook is *not*

- It does not promote the bench Mac to fleet membership. The bench Mac is
  the demo + grilling-session device per the 2026-05-21 decision; it
  enrols and decommissions on demand for testing and **must not** be left
  in the spreadsheet retirement set or counted against the ship gate.
- It does not "fix as you go". Anything that fails in § 5 / § 6 becomes a
  new issue, not an inline patch.
- It does not certify Wave 1. Pilot site is its own runbook
  ([phase-1-wave-1-pilot.md]; written when #13 runs).
