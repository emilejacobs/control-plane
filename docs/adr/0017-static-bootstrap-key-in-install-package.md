# ADR-017: Static bootstrap key bundled in install package

**Status:** Accepted (2026-05-21)

**Supersedes:** [ADR-014](./0014-bootstrap-token-via-s3.md)

**Context.** ADR-014 (three days prior) chose S3 with per-device one-time-use tokens for bootstrap distribution. That decision was made without the operational constraints surfaced during Phase 1 planning:

- **No per-device manual steps.** The whole point of Phase 1 is reducing HITL friction relative to the spreadsheet workflow. An engineer running `bin/issue-bootstrap-tokens-for-wave --site X` before each rollout batch reintroduces the friction we're trying to remove.
- **No engineer-maintained allowlist.** Same reason — relocates HITL, doesn't eliminate it.
- **Mosyle stays out of the runtime path.** CONTEXT.md is explicit: "Mosyle: Not in the CP runtime path; only triggers the install script." That rules out Mosyle-attested enrollment (CP querying Mosyle's API to validate the device's MDM status) and Mosyle custom-attribute distribution.
- **Linux devices have no MDM.** Whatever scheme is chosen must work for Pi/Radxa in Wave 3 (parallel track) without an MDM-attested fallback.

The space that remains: a credential bundled into the install package itself. Three shapes were considered:

1. **Per-device token, dispatched by Mosyle webhook → CP → S3 per ADR-014.** Requires a webhook capability in Mosyle and a runtime CP↔Mosyle integration. Violates the "Mosyle stays out of the runtime path" constraint.
2. **Bundled "ticket key" that requests an enrollment token via an indirection endpoint.** Adds a step but doesn't move the security needle — a compromised ticket key gives an attacker the same blast radius as a compromised enrollment key (request unlimited tokens, pollute the registry). Pure overhead.
3. **Static bootstrap key bundled in the install package, used directly for `POST /enrollments`.** Simplest; accepts a degraded security property (fleet-static vs per-device single-use); blast radius is bounded by architecture (see below).

**Decision.** Phase 1 distributes a single static bootstrap key bundled in the `mac-mini-rollout` install package (and the equivalent one-page Linux install script when Wave 3 runs). The install script presents this key to `POST /enrollments` alongside hostname + hardware UUID. The CP validates the key and mints a per-device mTLS cert in the response.

Concrete shape:

- **Storage of record:** AWS Secrets Manager, secret name `uknomi/cp/bootstrap-key`. Single secret value; rotation is a Secrets Manager `RotateSecret` operation.
- **Embedding:** the install-package CI in `mac-mini-rollout` fetches the secret at build time (IAM creds available to CI, not the device) and bakes the key into the resulting install package. The rollout repo's git history never contains the key.
- **Distribution:** Mosyle dispatches the resulting install package. Linux equivalent: the one-page Linux install script is rebuilt and re-published when keys rotate.
- **Rotation cadence:** every 6 months, by rebuilding the install package after rotating the Secrets Manager value. Cadence baked into a recurring runbook; does not require deploying code.
- **Enrollment endpoint hardening:**
  - Per-source-IP rate limit on `/enrollments` (20 requests/hour). Real waves are bursty over hours, not seconds.
  - Anomaly alert: hostnames that don't match the project naming convention (regex pinned in code, currently `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$`) raise an audit-log alert. Not an allowlist — a sanity check.
  - Page threshold: more than 10 enrollments from a single source IP in 10 minutes.
- **Audit:** every `/enrollments` request logs source IP, hardware UUID, hostname, success/failure, and (on success) the cert ARN minted. Mirrored to S3 daily per the audit-log mirror pattern.

**Consequences.**

- (+) Zero per-device manual steps. Install package is uniform across all Macs; Mosyle dispatches the same payload to every device.
- (+) No CP↔Mosyle runtime integration. Mosyle stays in its current role (dispatch only).
- (+) Works identically for Linux. Wave 3 install script bundles the same key, same flow.
- (+) Bounded blast radius. A stolen bootstrap key lets an attacker create rows in the `devices` table; it does **not** let them operate any real device. Per-device mTLS certs are minted at enrollment and bound to the thing identity created in that request. An attacker enrolling a fake device gets a cert for the fake device. Registry pollution is detectable (rate limit, anomaly alert, audit log) and recoverable (delete the fake rows).
- (+) Rotation has a known cost (rebuild install package, redistribute via Mosyle) and a known cadence (6 months). Painful but not heroic; the cadence makes the drill routine rather than emergency.
- (-) Fleet-static key — compromise means rotating across the entire install pipeline. ADR-014's per-device single-use scheme had no such fleet-wide rotation event.
- (-) Pollution attacks are possible. The mitigations (rate limit, anomaly alert, wave-engineer's dashboard-vs-spreadsheet check) are detection, not prevention. Acceptable given blast radius.
- (-) Operationally: the secret-fetching CI step adds a dependency between `mac-mini-rollout` CI and Secrets Manager. CI needs IAM creds scoped to read that one secret.

**Verification.** TBD — added at implementation. Integration tests cover:

- `tests/integration/enrollment_test.go::TestEnrollmentRejectsUnknownBootstrapKey` — 401 on wrong key.
- `tests/integration/enrollment_test.go::TestEnrollmentRateLimitTrips` — 21st request in an hour from a single source IP returns 429.
- `tests/integration/enrollment_test.go::TestEnrollmentAnomalyAlertOnBadHostname` — hostname not matching the naming-convention regex logs an alert event.
- `tests/integration/enrollment_test.go::TestEnrollmentMintsDeviceScopedCert` — successful enrollment returns a cert ARN, and the cert's policy binds to the newly-created thing only.
