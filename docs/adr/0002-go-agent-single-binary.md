# ADR-002: Go agent, single cross-compiled binary

**Status:** Accepted (2026-05-05)

**Context.** The agent runs on macOS (current and future) and Linux (deprecating Pis/Radxas). Languages considered: Swift (Mac-only), Python (already used in Edge UI), Node, Rust, Go.

**Decision.** Go. Single binary, cross-compiled to `darwin/arm64`, `darwin/amd64`, `linux/arm64`. Build-tag separated service backend abstracting `launchd` vs `systemd`.

**Consequences.**
- (+) Static binary — no runtime drift, no Python venv concerns, no Node version bumps.
- (+) Cross-compile is a one-line build matrix.
- (+) Solid IoT/MQTT library ecosystem.
- (+) launchd/systemd packaging is straightforward.
- (-) Team must be comfortable with Go (or willing to learn for this component).

Python rejected because the existing Edge UI (also Python) has caused runtime/dependency pain — the agent is the wrong place to repeat that. Swift rejected because Linux support is not viable. Rust rejected as overkill for the complexity level.

**Verification.** *(Added 2026-05-20 during Phase 0 cleanup. ADR-002 predates the template at `docs/agents/adr-template.md`, but its enforcement is now concrete.)*

- CI build matrix: `.github/workflows/ci.yml` cross-compiles `darwin/arm64`, `darwin/amd64`, and `linux/arm64` on every push and pull request; the workflow fails if any target fails to build.
- Local equivalent: `Makefile` `cross-build` target (`make cross-build`).
- Build-tag separation for the service backend lives in `internal/service/backend_darwin.go` (launchctl impl) and `internal/service/backend_linux.go` (systemctl impl); Go's build constraints exclude the wrong-platform file from each binary.
