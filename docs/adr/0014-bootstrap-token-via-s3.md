# ADR-014: Bootstrap token distribution via S3

**Status:** Accepted (2026-05-18)

**Context.** Enrollment (ADR-004) is install-script-driven. The script needs a one-time bootstrap token to call `POST /enrollments`. Two distribution options:

1. **S3** — a pre-signed URL deposits the token at a per-device path; install script fetches.
2. **Mosyle custom attribute** — Mosyle sets the token as a device attribute readable from the install script.

Mosyle only works for Macs (Pis/Radxas are not in Mosyle). The S3 option works for all OSes and matches an existing pattern in `mac-mini-rollout/modules/bootstrap-s3.sh`.

**Decision.** Bootstrap tokens are distributed via S3. The CP API generates a token, writes it to `s3://<bootstrap-bucket>/<device-hardware-id>/token` with a 1-hour expiry, and the install script fetches it as part of the enrollment flow. The token is one-time-use; it is consumed by the first successful `POST /enrollments` call (subsequent uses return 410 Gone).

**Consequences.**
- (+) Single distribution mechanism across Mac and Linux.
- (+) Pattern is already familiar from `bootstrap-s3.sh`.
- (+) Independent of Mosyle free-tier capabilities.
- (-) Requires the install script to have IAM-credentialed access to the bootstrap bucket. Handled via short-lived credentials or a per-device pre-signed URL.

**Verification.** TBD — added at implementation. Integration test covers: token issuance, single-use consumption, expiry after 1 hour, rejection of replay.
