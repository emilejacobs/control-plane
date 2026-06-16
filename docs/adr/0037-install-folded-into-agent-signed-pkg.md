# ADR-037: Install folded into the agent binary, delivered as a Mosyle-pushed signed pkg

**Status:** Accepted (2026-06-15)

**Extends:** [ADR-017](./0017-static-bootstrap-key-in-install-package.md) (static bootstrap key bundled in the install package). **Refines:** [ADR-004](./0004-install-script-enrollment.md) (install-script-driven enrollment).

**Context.**

The old install entry point was one of two Mosyle bootstrap scripts — `bootstrap-github.sh` (PAT clone) or `bootstrap-s3.sh` (presigned tarball) — that wrote a `.env` of **11 embedded credentials** and ran a Bash module framework (`setup.sh` + `modules/NN-*.sh`). It is brittle: a shared Tailscale key across the fleet, a 7-day presigned URL that silently expires mid-rollout, unchecked `REPLACE_` placeholders, and 4-way drift between `modules/11-cp-agent.sh` and the three `migrate-*` scripts (each hand-generates the same LaunchDaemon plist + agent-config). Two facts from the design pass (grill-with-docs, 2026-06-15) reshape the entry point: (a) `POST /enrollments` is on a public ALB and MQTT is public AWS IoT Core, so a fresh Mac needs **neither the tailnet nor any AWS credential** to enroll — only the bootstrap key; (b) the agent already self-updates via the [ADR-035](./0035-agent-fleet-update-mechanism.md) resident-wrapper supervisor.

**Decision.**

1. **Install is folded into the agent binary as a subcommand.** `uknomi-agent install` is the one-shot Provision ([ADR-036](./0036-cp-driven-device-lifecycle.md)); `uknomi-agent run` is the daemon under the supervisor. One Go codebase: the installer reuses the agent's CP client, mTLS, and typed config structs — eliminating the Bash-module framework and the 4-way plist/config-generation drift.

2. **Delivered as a Mosyle-pushed, signed (Developer ID + notarized) `.pkg`** that bundles the binary + the baked bootstrap key, with a postinstall that runs `uknomi-agent install`. Mosyle delivers and triggers only; the binary self-enrolls — **ADR-004 preserved** (enrollment is not MDM-driven).

3. **The bootstrap key is the only local secret.** No fetch credential — no GitHub PAT, no presigned URL, no S3 read creds. Every other secret is CP-delivered post-enrollment (ADR-036 Commission).

4. **The pkg carries only a bootstrapping binary.** Once enrolled, the agent self-updates to latest via ADR-035, so the pkg is rebuilt rarely — only when the bootstrap key rotates or the installer logic itself changes — not per agent release.

5. **The installer is idempotent by inspection** — it checks actual system state (Homebrew present? cert on disk? daemon loaded?) rather than a separate state-file ledger, removing the old inline-`python3` JSON state and its drift/race failure modes. Enrollment idempotency remains the `Idempotency-Key` (hardware UUID).

**Consequences.**

- (+) Local secret footprint collapses to one bootstrap key inside a signed, MDM-delivered artifact — killing both bootstrap scripts, the shared Tailscale key, and the presigned-URL expiry.
- (+) Install logic gains typed, testable Go and the agent's existing CP client; install bug-fixes ride the same self-update channel as the agent itself.
- (+) One artifact to sign — shared with the self-update signing machinery (ADR-035).
- (−) A signed + notarized pkg is more CI machinery than a shell script; rotating the bootstrap key means rebuilding + re-pushing the pkg (rare).
- (−) macOS-only. Pi/Radxa are legacy and explicitly out of scope (fleet direction).

**Verification.** TBD at implementation. Tests cover: `install` subcommand idempotency (re-run safety by inspection), enrollment via `Idempotency-Key`, and the pkg postinstall smoke path. Pkg signing/notarization + Mosyle delivery: `N/A — environmental/infra decision`.
