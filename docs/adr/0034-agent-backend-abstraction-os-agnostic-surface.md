# ADR-034: Agent backend abstraction for an OS-agnostic command + probe surface

**Status:** Accepted (2026-05-28)

**Context.** The Control Plane manages a mixed fleet — macOS (Mac Minis, the consolidation target) and the legacy Linux devices (Pis/Radxas, minimal-agent only per [ADR-007](./0007-pi-radxa-minimal-agent.md)). Two CP-side surfaces need to act on per-device state whose *implementation* is OS-specific but whose *meaning* is not:

1. **Service-control commands** (`service.restart`, `service.start`, `service.stop`) — Phase 3, riding the signed-command envelope ([ADR-013](./0013-agent-self-update-phase-3.md), narrowed by [ADR-028](./0028-unsigned-config-update-phase-2.md)). "Restart the transcriber" means `launchctl kickstart -k` on macOS and `systemctl restart` on Linux.
2. **Health probes** (`.scratch/phase-2-fleet-health-probes/PRD.md`, issue #19) — Phase 2. "Is auto-login configured?" is `defaults read … autoLoginUser` + `/etc/kcpassword` on macOS and a `getty@`/display-manager check on Linux.

The agent already demonstrates the right shape for surface #1: `internal/service/backend.go` defines a two-method `Backend` interface (`Status`, `Restart`) with `backend_darwin.go` (launchctl) and `backend_linux.go` (systemctl) implementations selected at build time. CP transmits a service *name* and a *verb*; it never sees `launchctl` or `systemctl`.

The risk this ADR addresses: without an explicit, written rule, a contributor extending either surface could bake an OS-specific verb into CP-side code — a probe stored as `launchd_job_loaded`, a dashboard column reading `systemctl_active`, an API that accepts `launchctl` arguments. Each such leak forces a refactor the day a second OS (or a replacement OS) needs the same surface, and it pushes device-implementation detail into the operator-facing API and dashboard, where it does not belong. The agent backend split exists today by convention; it has never been stated as a constraint that governs *new* surfaces.

Alternatives considered:
- **Per-OS API surfaces** (CP exposes `macos/probes` vs `linux/probes`) — rejected: doubles the API, dashboard, and storage schema, and couples the operator surface to fleet composition.
- **OS-specific values stored, translated at read time in the dashboard** — rejected: pushes the translation into application code on the CP side and into TypeScript, the worst place to keep an OS matrix in sync.
- **Leave it as convention** — rejected: convention is invisible to an AFK agent reading the code cold; under architectural-reviewer-only dev (see the ADR-template rationale) the constraint must be written and ideally enforced.

**Decision.**

The CP-side surface is **OS-agnostic**. This applies to every layer CP owns:

- **REST API** — command names, probe names, and request/response shapes carry no OS-specific verb. `GET /devices/{id}/health-probes` returns probe-name → state; the signed-command envelope carries `service.restart` + a logical service name.
- **Stored vocabulary** — Postgres column values (probe `state`, service `state`, the OS-agnostic probe/command identifiers) are constant across OSes. Per-OS structured detail, when needed, lives in a `details_jsonb` column — never in the identifier or the canonical state value.
- **Dashboard** — renders probe/command names and states as received; contains no `if macOS` branch on OS-specific verbs.

Per-OS implementation lives **behind agent-side Go backend interfaces**, one implementation file per OS, selected by build tag:

- `service.Backend` (`internal/service/backend.go`) — exists today; this ADR ratifies it as the canonical pattern.
- `probes.Backend` (new, issue #19) — mirrors it: an interface in `internal/probes/backend.go`, with `backend_darwin.go` shipping in slice 1 and `backend_linux.go` deferred per [ADR-007](./0007-pi-radxa-minimal-agent.md) but with the interface OS-agnostic from day one.
- Any future agent surface that touches OS-specific state follows the same shape.

The agent is the **only** component that knows OS-specific verbs (`launchctl`, `systemctl`, `kcpassword`, `defaults`, `system_profiler`, `ioreg`, `lsusb`, `docker`, `brew`, `open`). It translates an OS-agnostic name into an OS-specific check or command. CP never transmits, stores, or renders those verbs.

The OS-agnostic vocabulary is a **contract**: a probe or command name must mean the same operator-facing thing on every OS that implements it. The cross-OS mapping table in the fleet-health-probes PRD is the spec for the eventual Linux probe backend; an equivalent mapping governs service-control.

**Consequences.**
- (+) Adding the Linux backend (or any future OS) is a backend swap — a new `backend_<os>.go` implementing the existing interface — not a refactor of the API, storage schema, or dashboard.
- (+) The operator-facing surface stays clean: operators and the future mobile app see "auto-login: missing", not "kcpassword absent". Fleet composition (how many Macs vs Pis) never leaks into the API.
- (+) Low marginal cost — `service.Backend` already proves the pattern; `probes.Backend` is a direct mirror, so #19 inherits a known shape rather than inventing one.
- (+) Locks the principle across both Phase 2 (probes) and Phase 3 (service-control) before either surface's second-OS implementation lands, when the cost of a leaked verb is highest.
- (-) The OS-agnostic vocabulary must be kept in sync across backends — a probe added to the darwin backend defines a contract every other backend must honour with the same meaning, or the name lies on some OS.
- (-) Some OS-specific richness is lost from the canonical state value and must be pushed into `details_jsonb`, which is less queryable than a first-class column.
- (-) The constraint is easy to violate by accident (a well-meaning probe named after its macOS check); it needs enforcement, not just documentation — see Verification.

**Verification.** TBD — added at implementation of issue #19. Planned enforcement:
- `internal/probes/backend.go` defines the `probes.Backend` interface; `backend_darwin.go` implements it under a `darwin` build tag, mirroring `internal/service/backend.go`.
- A test asserts the CP-side API and storage layer contain no OS-specific verb strings (`launchctl`, `systemctl`, `kcpassword`, `defaults`, `system_profiler`, etc.) — a guard test in the cp-api / cp-ingest packages, analogous to the idempotency guard in `tests/integration/`.
- The probe/command identifier set is defined once (Go constants) and shared between agent and CP ingest, so a name cannot drift between the two sides.
