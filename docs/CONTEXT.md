# Glossary

Terms used throughout the design documents.

| Term | Meaning |
|---|---|
| **CP** | Control Plane. This project — the AWS-hosted system. |
| **Edge device** | Any uKnomi-managed hardware at a client site (Mac Mini, Raspberry Pi, Radxa Rock). |
| **Edge UI** | The Flask app running on each Mac at `localhost:5050`. Formerly named "Talon"; renamed to "uKnomi Edge". Lives in `mac-mini-rollout/webui/`. |
| **Agent** | `uknomi-agent`, the Go binary on every device that talks to the CP via MQTT. |
| **Tailnet** | The uKnomi Tailscale network. All edge devices and the CP's Tailscale subnet router are members. |
| **Subnet router** | A Tailscale node that advertises a route, allowing other tailnet members to reach hosts behind it. The CP runs one as a Fargate task to give the API service tailnet access without enrolling clients. |
| **Device shadow** | An IoT Core feature representing desired and reported state of a thing as JSON documents. CP uses it for service-state tracking. |
| **Bootstrap token** | A short-lived, one-time-use credential the install script uses to enroll a device with the CP. |
| **Enrollment** | The one-time process of registering a new device with the CP and provisioning its mTLS cert. |
| **Signed command** | A command payload signed with the CP's Ed25519 key (held in KMS) before being sent to a device. The agent verifies the signature before executing. |
| **Site** | A physical client location. One client may have multiple sites. |
| **Operator (staff)** | A uKnomi internal user who logs in via Entra ID and has full fleet access. |
| **Operator (local)** | A future field-operator user with a local CP account, scoped to specific sites. |
| **Mosyle** | Apple MDM provider used for Mac auto-enrollment. Not in the CP runtime path; only triggers the install script. |
