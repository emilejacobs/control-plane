# ADR-004: Install-script-driven enrollment, not MDM-driven

**Status:** Accepted (2026-05-05)

**Context.** Enrollment options: Mosyle webhook on device add, Mosyle API polling, install-script self-enrollment. Mosyle is on the free tier and may not expose webhooks; Pis/Radxas are not in Mosyle at all.

**Decision.** Devices enroll themselves at install time. The install script fetches a one-time bootstrap token from S3, calls `POST /enrollments`, and receives a per-device mTLS cert. Mosyle's role is reduced to "the thing that triggers `setup.sh` on a Mac" — same role as the manual flash on a Pi.

**Consequences.**
- (+) Single enrollment flow across all device types.
- (+) Independent of Mosyle tier capabilities.
- (+) Install scripts already do S3 fetches (`bootstrap-s3.sh` exists) — pattern is familiar.
- (-) No automatic decommission when Mosyle removes a device. Mitigated by a periodic reconciliation job (Phase 4) and manual decommission via dashboard (Phase 3).
