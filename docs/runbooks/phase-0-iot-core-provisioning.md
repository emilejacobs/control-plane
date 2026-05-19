# Phase 0 — Manual IoT Core provisioning

How to provision an AWS IoT Core thing + mTLS cert for a single device, by hand. This runbook covers the manual flow used during Phase 0. Phase 1 replaces it with the `POST /enrollments` endpoint (see ADR-004) and the `mac-mini-rollout` install module.

## When to use this

- Bringing up the developer laptop as the very first test "device" for Issue 01.
- Bringing up the field-deployment Mac (Issue 07) or Linux device (Issue 08).

## Prerequisites

- AWS CLI v2 configured with credentials that can use IoT Core in the chosen region (`aws configure`).
- `openssl` installed (macOS and Linux ship with it).
- The agent binary built (`go build -o bin/agent ./cmd/agent`).

Pick a region and stick with it for the whole flow (`us-east-1` recommended for Phase 0). All `aws iot ...` commands accept `--region`; set `AWS_REGION` to avoid typing it every time:

```bash
export AWS_REGION=us-east-1
```

Pick a **device identifier** — this is the IoT Core "thing name" and the agent's `device_id`. Phase 0 convention: `dev-<hostname-or-handle>` for the developer laptop, or `prod-<site>-<hostname>` for field devices.

```bash
export DEVICE_ID=dev-laptop-emile
```

## 1. Create the IoT policy (once per AWS account)

The policy authorises a device cert to publish/subscribe **only on its own topics**. Topic-scoping uses the cert's IoT thing name via `${iot:Connection.Thing.ThingName}`.

```bash
cat > /tmp/uknomi-agent-policy.json <<'EOF'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "iot:Connect",
      "Resource": "arn:aws:iot:*:*:client/${iot:Connection.Thing.ThingName}"
    },
    {
      "Effect": "Allow",
      "Action": ["iot:Publish", "iot:Receive"],
      "Resource": [
        "arn:aws:iot:*:*:topic/devices/${iot:Connection.Thing.ThingName}/cmd",
        "arn:aws:iot:*:*:topic/devices/${iot:Connection.Thing.ThingName}/cmd-result",
        "arn:aws:iot:*:*:topic/devices/${iot:Connection.Thing.ThingName}/telemetry"
      ]
    },
    {
      "Effect": "Allow",
      "Action": "iot:Subscribe",
      "Resource": "arn:aws:iot:*:*:topicfilter/devices/${iot:Connection.Thing.ThingName}/cmd"
    }
  ]
}
EOF

aws iot create-policy \
  --policy-name UknomiAgentPolicy \
  --policy-document file:///tmp/uknomi-agent-policy.json
```

If the policy already exists, this returns `ResourceAlreadyExistsException` — safe to ignore.

## 2. Create the thing

```bash
aws iot create-thing --thing-name "$DEVICE_ID"
```

## 3. Generate and register the device cert + key

IoT Core can either accept your own CA-signed certs or issue its own. Phase 0 uses the simpler option: let IoT Core generate the keypair.

```bash
mkdir -p ./certs/$DEVICE_ID

aws iot create-keys-and-certificate \
  --set-as-active \
  --certificate-pem-outfile ./certs/$DEVICE_ID/cert.pem \
  --public-key-outfile      ./certs/$DEVICE_ID/public.key \
  --private-key-outfile     ./certs/$DEVICE_ID/private.key \
  --query 'certificateArn' \
  --output text \
  > ./certs/$DEVICE_ID/arn.txt
```

The cert ARN is now in `./certs/$DEVICE_ID/arn.txt`. The cert is **active** in IoT Core (`--set-as-active`).

> **Cert TTL note (ADR-013):** Certs minted by `create-keys-and-certificate` are valid for ~1 year by default — matches the Phase 1 cert TTL decision. Rotation is a Phase 4 deliverable.

## 4. Attach the policy and thing to the cert

```bash
export CERT_ARN=$(cat ./certs/$DEVICE_ID/arn.txt)

aws iot attach-policy --policy-name UknomiAgentPolicy --target "$CERT_ARN"
aws iot attach-thing-principal --thing-name "$DEVICE_ID" --principal "$CERT_ARN"
```

## 5. Fetch the IoT Core CA root and the broker endpoint

The agent needs the Amazon Root CA cert to verify the broker's server cert during the TLS handshake.

```bash
curl -o ./certs/AmazonRootCA1.pem \
  https://www.amazontrust.com/repository/AmazonRootCA1.pem
```

Get the regional MQTT endpoint (this is account- and region-specific):

```bash
aws iot describe-endpoint --endpoint-type iot:Data-ATS --output text
# example output: a1b2c3d4e5f6g7-ats.iot.us-east-1.amazonaws.com
```

Combine with the MQTT-over-WSS port to form the broker URL:

```
tls://<endpoint>:8883
```

## 6. Write the agent config

```bash
cat > ./certs/$DEVICE_ID/config.json <<EOF
{
  "device_id":    "$DEVICE_ID",
  "version":      "0.1.0",
  "broker_url":   "tls://$(aws iot describe-endpoint --endpoint-type iot:Data-ATS --output text):8883",
  "client_id":    "$DEVICE_ID",
  "cert_path":    "$(pwd)/certs/$DEVICE_ID/cert.pem",
  "key_path":     "$(pwd)/certs/$DEVICE_ID/private.key",
  "ca_cert_path": "$(pwd)/certs/AmazonRootCA1.pem"
}
EOF
```

## 7. Run the agent

```bash
./bin/agent --config ./certs/$DEVICE_ID/config.json
```

You should see structured JSON logs on stderr, ending with:

```json
{"level":"INFO","msg":"agent started","device_id":"dev-laptop-emile",...}
```

## 8. Send a heartbeat from another terminal

```bash
./bin/agent-cli \
  --broker "tls://$(aws iot describe-endpoint --endpoint-type iot:Data-ATS --output text):8883" \
  --ca   ./certs/AmazonRootCA1.pem \
  --cert ./certs/$DEVICE_ID/cert.pem \
  --key  ./certs/$DEVICE_ID/private.key \
  --device $DEVICE_ID \
  --command heartbeat
```

Expected output (pretty-printed JSON):

```json
{
  "correlation_id": "<random>",
  "command_id":     "<random>",
  "success":        true,
  "result":         "<base64 or JSON object>"
}
```

The `result` field decodes to `{"device_id": "dev-laptop-emile", "version": "0.1.0", "os": "darwin", "uptime_seconds": <n>}`.

> **Note on the CLI's own cert:** The CLI uses the same device cert + key + CA as the agent itself. The IoT policy above scopes by `${iot:Connection.Thing.ThingName}` which equals the cert's attached thing name. Using the device's own cert means the CLI shows up on IoT Core as the device itself, which is fine for Phase 0 testing but **must not** be done in production — for that we'll issue a separate "controller" cert with broader pub/sub permissions (Phase 1).

## Teardown

To remove a single device's resources cleanly:

```bash
aws iot detach-thing-principal --thing-name "$DEVICE_ID" --principal "$CERT_ARN"
aws iot detach-policy --policy-name UknomiAgentPolicy --target "$CERT_ARN"
CERT_ID=$(echo "$CERT_ARN" | awk -F/ '{print $NF}')
aws iot update-certificate --certificate-id "$CERT_ID" --new-status INACTIVE
aws iot delete-certificate --certificate-id "$CERT_ID"
aws iot delete-thing --thing-name "$DEVICE_ID"
rm -rf ./certs/$DEVICE_ID
```

The policy stays — it's account-wide and reusable for the next device.

## Troubleshooting

- **`AccessDeniedException` on connect:** the policy doesn't allow `iot:Connect` for the thing name, or the cert isn't attached to a thing. Check steps 1, 2, and 4.
- **TLS handshake fails:** `AmazonRootCA1.pem` is missing or stale. Re-fetch from `https://www.amazontrust.com/repository/AmazonRootCA1.pem`.
- **Agent connects but commands time out:** the IoT policy may not allow the `cmd` topic — verify the policy ARN list includes the `topic/devices/${iot:Connection.Thing.ThingName}/cmd` and `topicfilter/...` entries.
- **`describe-endpoint` returns a different endpoint each time:** it shouldn't. If it does, your account has multiple endpoints — pin to `iot:Data-ATS` (ATS-signed) as shown above; it's the modern default.
