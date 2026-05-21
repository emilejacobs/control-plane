# Issue 06 — LaunchDaemon plist + systemd unit file

Status: ready-for-human

## Parent

PRD: [`../PRD.md`](../PRD.md)

## What to build

The OS service unit files that allow the agent to run as a managed service on both macOS and Linux: start on boot, restart on crash, brief backoff between restart attempts. Phase 0 ships these as static files in `deploy/` (or similar); Phase 1's `mac-mini-rollout/modules/11-cp-agent.sh` will template them later.

Scope:

- macOS LaunchDaemon plist (e.g. `com.uknomi.agent.plist`) intended for installation under `/Library/LaunchDaemons/`.
  - Runs as a system service (not a per-user LaunchAgent — Phase 0 deployment is system-level).
  - `RunAtLoad` true, `KeepAlive` true with `SuccessfulExit: false` semantics so the agent restarts on crash but not on clean shutdown.
  - Sensible `ThrottleInterval` (~5s) to avoid restart storms.
- Linux systemd unit (e.g. `uknomi-agent.service`) intended for installation under `/etc/systemd/system/`.
  - `Restart=on-failure`, `RestartSec=5s`, `WantedBy=multi-user.target`.
  - Runs as a system service; runtime user TBD by the implementer (root acceptable for Phase 0).
- Install steps documented in markdown precise enough to follow without ad-hoc reasoning. Should cover: where to copy each file, how to load/enable, how to view logs on each OS (`launchctl print` on Mac, `journalctl -u uknomi-agent` on Linux).
- Static files only — no scripting or templating. Phase 1 picks that up.

## Acceptance criteria

- [ ] Plist and systemd unit files exist in the repo at agreed paths.
- [ ] Install steps are documented in markdown for both OSes.
- [ ] On a dev laptop (Mac), installing the plist and rebooting causes the agent to start automatically.
- [ ] On a Linux test box, installing the systemd unit and rebooting causes the agent to start automatically.
- [ ] Manually killing the agent process causes it to restart within the configured backoff window on both OSes.
- [ ] Smoke test for "agent comes back after device reboot" is documented as part of Issues 07 and 08 — this issue's bar is "works on a dev box and the install docs are correct."

## Blocked by

- [Issue 01 — First light](./01-first-light.md)
