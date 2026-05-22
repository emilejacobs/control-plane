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
| **Bootstrap key** | A static shared secret bundled into the device install package; the install script presents it to `POST /enrollments`. Static-key approach per ADR-017, which superseded the short-lived per-device S3 token of ADR-014. |
| **Enrollment** | The one-time process of registering a new device with the CP and provisioning its mTLS cert. |
| **Signed command** | A command payload signed with the CP's Ed25519 key (held in KMS) before being sent to a device. The agent verifies the signature before executing. |
| **Site** | A physical client location. One client may have multiple sites. |
| **Operator (staff)** | A uKnomi internal user with full fleet access. Authenticates with local credentials — password + TOTP (ADR-010, which dropped Entra ID); carries the `'*'` site allowlist via the `is_staff` flag. |
| **Operator (local)** | A future field-operator user with a local CP account, scoped to specific sites. |
| **Mosyle** | Apple MDM provider used for Mac auto-enrollment. Not in the CP runtime path; only triggers the install script. |
| **Heartbeat** | A small message the agent publishes to `devices/{id}/telemetry` every 30s. The ingest worker uses it to update the device's `last_seen` in Postgres. |
| **Online threshold** | The freshness window for `last_seen` that makes a device count as "online" in the dashboard. Phase 1 value: 90 seconds (3× heartbeat interval). |
| **Presence** | The online/offline state of a device as shown in the dashboard. Derived from `last_seen` against the online threshold, with IoT Core lifecycle events as a fast-path for the online → offline transition. |
| **Recovery code** | One of ten single-use codes issued to an operator at TOTP enrollment time. Shown once, stored hashed, used to recover access if the TOTP device is lost. |
| **First-run admin** | The bootstrap pattern by which the very first operator account is created. The dashboard exposes an account-creation flow at `/auth/first-run` if and only if no users exist in the database. After the first admin is created, the flow is permanently disabled (a `system_initialized` flag flips). |
