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
| **Client** | A uKnomi customer organization. Owns one or more sites — the `clients` and `sites` tables. |
| **Site** | A physical client location. One client may have multiple sites. |
| **Operator (staff)** | A uKnomi internal user with full fleet access. Authenticates with local credentials — password + TOTP (ADR-010, which dropped Entra ID); carries the `'*'` site allowlist via the `is_staff` flag. |
| **Operator (local)** | A future field-operator user with a local CP account, scoped to specific sites. |
| **Site allowlist** | The set of sites an operator may see. Staff hold the full fleet implicitly via `is_staff`; a non-staff operator's allowlist is the explicit `operator_sites` grants. Enforced on every device read through the `authz` module's `ScopedDeviceQuery` helper. |
| **Mosyle** | Apple MDM provider used for Mac auto-enrollment. Not in the CP runtime path; only triggers the install script. |
| **Heartbeat** | A small message the agent publishes to `devices/{id}/telemetry` every 30s. The ingest worker uses it to update the device's `last_seen` in Postgres. |
| **Service-status report** | A typed message the agent publishes to `devices/{id}/service-status` every 5 minutes (Phase 2). Carries the observed `running | stopped | unknown` state of every service in the agent's per-device `service_allow_list`. cp-ingest persists rows into `device_services`; the dashboard's per-device Services panel renders them. Wire type: `internal/protocol/servicestatus.Report`. |
| **Allow-list (service)** | The per-device list of launchd/systemd unit names the agent reports status for. Configured in the agent's JSON config (`service_allow_list`). Starts from the install module's default; an operator can override it from the dashboard (see *Service allow-list override*). |
| **Service allow-list override** | The per-device customisation of the agent's allow-list + reporting cadence stored on `devices.service_allow_list_override` + `devices.service_status_interval_override`. Set via `PUT /devices/{id}/service-config` (cp publishes `config.update` on `devices/{id}/cmd`; agent hot-reloads its collector + publisher and ACKs on `cmd-result`). `null` override means "use the install default"; explicit `[]` override means "track nothing". The dashboard's Services Card shows `(overridden)` vs `(default)` derived from this. Phase 2 slice 2. |
| **`config.update`** | The first downward CP→agent cmd type (Phase 2 slice 2). Strictly scoped to `service_allow_list` + `service_status_interval` by the agent's `internal/handlers/configupdate.Handler`. Unsigned in Phase 2 (ADR-028); Phase 3 wraps the same envelope in the signed shape. |
| **`device_services`** | The per-(device, service_name) Postgres table holding the latest reported state. PK `(device_id, service_name)`; ON DELETE CASCADE clears rows when a device is decommissioned. State column is `text` (not enum) so Phase 3 can add `failed` without a migration. |
| **Online threshold** | The freshness window for `last_seen` that makes a device count as "online" in the dashboard. Phase 1 value: 90 seconds (3× heartbeat interval). |
| **Presence** | The online/offline state of a device, stored as `devices.is_online` and maintained by `cp-ingest`: a heartbeat or an IoT Core `connected` event sets it online; an IoT Core `disconnected` event, or the sweeper finding `last_seen` older than the online threshold, sets it offline. |
| **Recovery code** | One of ten single-use codes issued to an operator at TOTP enrollment time. Shown once, stored hashed, used to recover access if the TOTP device is lost. |
| **First-run admin** | The bootstrap pattern by which the very first operator account is created. The dashboard exposes an account-creation flow at `/auth/first-run` if and only if no users exist in the database. After the first admin is created, the flow is permanently disabled (a `system_initialized` flag flips). |
