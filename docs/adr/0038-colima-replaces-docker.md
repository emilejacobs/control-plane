# ADR-038: Colima replaces Docker Desktop; per-user VM, root agent drives via launchctl asuser

**Status:** Accepted (2026-06-15)

**Supersedes:** the Docker Desktop approach in `mac-mini-rollout/modules/52-plate-recognizer.sh`.

**Context.**

Plate Recognizer Stream (ALPR) runs as a container. Under Docker Desktop this requires a GUI license-acceptance click (un-scriptable), a `settings-store.json` license-injection hack, and a `docker-watchdog.sh` launchd agent — the single worst manual step in the install, and a recurring ongoing-management headache. **Colima** (MIT-licensed, CLI-only, Docker-socket-compatible) was validated on a Mac mini (`plate-recognizer-colima-test.sh`): `vz` backend, `--mount` for the `config.ini` host round-trip, `--restart=unless-stopped`. The wrinkle: Colima runs a **per-user VM as the `uknomi` user**, while the agent + supervisor run as a **root LaunchDaemon**.

**Decision.**

1. **Colima replaces Docker Desktop** for the ALPR container, fleet-wide. Installed via Homebrew (the `colima` + `docker` CLI **formulae**, not the Docker cask), `vz` backend, per-user VM.

2. **The uid boundary is resolved by split ownership.** A **user LaunchAgent** in the auto-logged-in `uknomi` session brings Colima up at login, and the container runs `--restart=unless-stopped` — so the VM + workload survive independently of the agent. The **root agent manages only the workload**: it writes the ALPR `config.ini` and runs `docker`/`colima` commands by dropping into the user session via `launchctl asuser $(id -u uknomi) sudo -u uknomi …`. The `log.tail` `docker` kind resolves the same way.

3. **Auto-login (`uknomi`, via kcpassword) is load-bearing for ALPR** — no GUI session, no Colima VM. The agent reports **Colima + the container as services** in its `service_allow_list`, so the CP sees a VM/container outage rather than silently losing ALPR.

4. **Existing Docker devices migrate to Colima via the operator-watched SSH harness** (`migrate-fleet.sh`, [ADR-036](./0036-cp-driven-device-lifecycle.md) §6), not an unattended fleet command — swapping a live store device's container runtime is invasive enough to want a human watching per-device.

**Consequences.**

- (+) Removes the Docker license click + `settings-store.json` hack + `docker-watchdog` — the worst un-scriptable install step is gone, and ongoing management simplifies.
- (+) Container lifecycle is independent of agent restarts/self-updates.
- (+) Open-source runtime, no per-seat licensing.
- (−) ALPR availability is now coupled to auto-login + an active GUI session; a stuck login takes ALPR down (mitigated by the service-status visibility in §3).
- (−) The root↔user boundary adds `launchctl asuser` indirection to every container operation.
- (−) A Lima VM's resource footprint (sized per the test script: 2 CPU / 4 GiB / 30 GiB) on each ALPR Mac.

**Verification.** TBD at implementation. Tests cover: agent container ops via `launchctl asuser` (start / restart / logs), the `config.ini` write + mount round-trip, and Colima/container service-status reporting. VM provisioning: `N/A — environmental/infra decision`.
