# Issue 05 — LaunchAgent dual-context Status fallback

Status: ready-for-agent
Type: AFK
Estimate: 4–6 hr

## Parent

- Source: live observation on bench Mac `bbe0540c…` after the user added a LaunchAgent to the slice-2 allow-list and the dashboard showed it permanently as `unknown`.
- Prior handoff captured the analysis under § LaunchAgent investigation: the agent runs as root via LaunchDaemon, so `launchctl list <name>` only sees the **system** namespace and never finds jobs registered under `gui/<uid>/`.
- ADRs to honour: ADR-022 (per-service rows + state semantics — `unknown` is only for transient failure; a job that exists in the GUI context should not stay `unknown`).

## Root cause

`internal/service/backend_darwin.go:21-45` shells out to `launchctl list <name>` from root context. macOS launchd partitions jobs by domain (`system/`, `gui/<uid>/`, `user/<uid>/`, …) and `launchctl list` only inspects the caller's current domain. Result: every LaunchAgent allow-list entry returns ErrNotFound → collector silences (per slice 1 design, ErrNotFound is the expected "absent service" case) → wire state is `StateUnknown` with no log line to diagnose.

## What to build

Add a GUI-context fallback to `launchctlBackend.Status` that fires only when the system query returns ErrNotFound. Keep the existing system-context path unchanged for the common case (LaunchDaemons, which is what almost every allow-listed service is today).

### Scope

- Introduce a `runner` function field and a `consoleUID` function field on `launchctlBackend` so the shell-outs can be faked in unit tests. Production constructor (`NewSystemBackend`) wires both to real implementations.
- New fallback: when system-context `launchctl list <name>` returns a non-zero exit, look up the GUI uid via `os.Stat("/dev/console").Sys().(*syscall.Stat_t).Uid` (the macOS convention for "uid currently logged into the GUI"), then shell out to `launchctl print gui/<uid>/<name>`. Parse the `state = running|not running|waiting` line.
  - `state = running` → `StateRunning`
  - `state = not running` / `state = waiting` / anything else → `StateStopped`
  - non-zero exit → `ErrNotFound` (silent, matches existing semantics)
- When the GUI fallback finds the service, emit a debug log line including `service`, `gui_uid`, and observed `state` so the existence of the GUI-context job is diagnosable. Use the `*slog.Logger` already plumbed through the collector — backend doesn't currently take a logger so add one as a constructor option, defaulting to discard.
- When `os.Stat("/dev/console")` fails (e.g. no graphical login session active), skip the fallback and return ErrNotFound with a single debug log line. Don't surface as warn — there's nothing actionable.

### Out of scope

- `Restart` does not get a GUI-context fallback. Restart is currently not observably broken (no fleet user reports a LaunchAgent restart failure yet), and the namespace handling for `launchctl kickstart gui/<uid>/<name>` adds complexity. File a sub-issue if/when the gap matters.
- Multi-user / fast-user-switch scenarios. Single-Mac kiosk posture (handoff § Single-Mac focus) makes single-uid-at-/dev/console a safe assumption.

## Acceptance criteria

- [ ] `internal/service/backend_darwin_test.go` exercises: (1) system context returns running, no GUI call made; (2) system ErrNotFound + GUI state=running → StateRunning; (3) system ErrNotFound + GUI state=not running → StateStopped; (4) both contexts ErrNotFound → ErrNotFound; (5) consoleUID error → ErrNotFound, no panic.
- [ ] Linux backend unchanged.
- [ ] Bench Mac roll: hot-swap new agent binary; user-context LaunchAgent in allow-list flips from `unknown` to its actual state within one collector tick.
- [ ] cp-ingest logs show no new noise (no ErrNotFound warn-spam from the fallback path).
- [ ] **Documentation updated.** No ADR change — this is an implementation-level fix, not an architectural one. Add a sentence to `docs/architecture.md` § Edge agent / Service collector if it currently describes the launchctl path.

## Blocked by

- None.

## Related future work

- LaunchAgent **Restart** dual-context fallback. File as a sub-issue when slice 3 of Phase 3 (signed `service.restart`) lands or when a fleet user reports a stuck LaunchAgent.
- `state = waiting` distinction — could surface as a new wire state (`StateWaiting`?) if operators want to distinguish "launchd is going to start this soon" from "this is stopped". Today both collapse to `StateStopped`. Defer until someone asks.
