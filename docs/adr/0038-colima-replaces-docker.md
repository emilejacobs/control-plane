# ADR-038: Colima replaces Docker Desktop; per-user VM, root agent drives via launchctl asuser

**Status:** Accepted (2026-06-15)

**Supersedes:** the Docker Desktop approach in `mac-mini-rollout/modules/52-plate-recognizer.sh`.

**Context.**

Plate Recognizer Stream (ALPR) runs as a container. Under Docker Desktop this requires a GUI license-acceptance click (un-scriptable), a `settings-store.json` license-injection hack, and a `docker-watchdog.sh` launchd agent — the single worst manual step in the install, and a recurring ongoing-management headache. **Colima** (MIT-licensed, CLI-only, Docker-socket-compatible) was validated on a Mac mini (`plate-recognizer-colima-test.sh`): `vz` backend, `--mount` for the `config.ini` host round-trip, `--restart=unless-stopped`. The wrinkle: Colima runs a **per-user VM as the `uknomi` user**, while the agent + supervisor run as a **root LaunchDaemon**.

**Decision.**

1. **Colima replaces Docker Desktop** for the ALPR container, fleet-wide. Installed via Homebrew (the `colima` + `docker` CLI **formulae**, not the Docker cask), `vz` backend, per-user VM.

2. **The uid boundary is resolved by split ownership.** A **user LaunchAgent** in the auto-logged-in `uknomi` session brings Colima up at login, and the container runs `--restart=unless-stopped` — so the VM + workload survive independently of the agent. The **root agent manages only the workload**: it writes the ALPR `config.ini` and runs `docker`/`colima` commands by dropping into the user session via `launchctl asuser $(id -u uknomi) sudo -u uknomi …`. The `log.tail` `docker` kind resolves the same way.

3. **Auto-login (`uknomi`, via kcpassword) is load-bearing for ALPR** — no GUI session, no Colima VM. The agent reports **Colima + the container as services** in its `service_allow_list`, so the CP sees a VM/container outage rather than silently losing ALPR.

4. **Existing Docker devices migrate to Colima via the operator-watched SSH harness** (`migrate-colima.sh`, [ADR-036](./0036-cp-driven-device-lifecycle.md) §6), not an unattended fleet command — swapping a live store device's container runtime is invasive enough to want a human watching per-device.

5. **LAN reachability requires `--network-address --network-preferred-route`** (added 2026-06-19). The ALPR container must reach its RTSP camera on the store LAN — a host *directly connected* on the Mac's Ethernet (e.g. `192.168.43.6`). Colima's default networking — and even plain `--network-address` — is NAT (lima usernet / VZNAT) that reaches the internet and the host but **not** other hosts on the directly-connected subnet, so the container silently fails camera capture (ALPR logs `URL unreachable`, disables the camera, exits). `--network-preferred-route` makes the VZNAT *reachable* network the VM's default route, which **does** reach the LAN. Stays in **shared mode** — no `socket_vmnet`, no bridged interface, no sudoers — preserving the "no privileged daemon" benefit (bridged/`socket_vmnet` was evaluated and is **not** needed). Both `migrate-colima.sh`'s `colima start` and the `com.uknomi.colima` LaunchAgent carry these two flags.

6. **The VM is sized to the host, not fixed** (added 2026-06-19; supersedes the 2 CPU / 4 GiB test-script sizing). ALPR plate inference is CPU-bound and Docker Desktop had given the container ~all the host's cores; a fixed 2 vCPU VM tanks recognition health (observed dropping 100%→50% with `cpu_count: 2`). Size per device: **CPU = cores − 2** (leave 2 for macOS/agent/edge-ui), **RAM = ½ host capped 4–8 GiB** (ALPR's footprint is small), disk 30 GiB (screenshots/clips ride the host bind mount). On an M4 mini (10c/16 GiB) → 8 CPU / 8 GiB. Implemented in `install.ColimaVMSize` (install path) and mirrored in `migrate-colima.sh` (migration path).

**Consequences.**

- (+) Removes the Docker license click + `settings-store.json` hack + `docker-watchdog` — the worst un-scriptable install step is gone, and ongoing management simplifies.
- (+) Container lifecycle is independent of agent restarts/self-updates.
- (+) Open-source runtime, no per-seat licensing.
- (−) ALPR availability is now coupled to auto-login + an active GUI session; a stuck login takes ALPR down (mitigated by the service-status visibility in §3).
- (−) The root↔user boundary adds `launchctl asuser` indirection to every container operation.
- (−) A Lima VM's resource footprint (sized per §6: CPU = cores−2, RAM = ½ host capped 4–8 GiB) on each ALPR Mac.
- (−) ALPR camera capture depends on the VZNAT preferred-route path reaching the LAN (§5); a host on Wi-Fi-only with an unusual route could need revisiting (fleet is Ethernet Mac minis).

**Verification.** Validated on a test M4 mini (2026-06-19): migration preserved config + license; **container reaches the LAN camera** (`192.168.43.6:554`) under §5's flags (NAT could not); recognition **health holds ~100%** at §6 sizing (2 vCPU dropped it to ~50%); Colima auto-starts at reboot via the LaunchAgent; and CP reports `plate_recognizer_container: running` + live `log.tail` via the Colima-aware agent (agent-v1.5.2, probe/log.tail through `launchctl asuser … --context colima`). Unit tests cover: `ColimaVMSize`, the LaunchAgent plist flags, the Colima-aware probe (+ Docker-Desktop fallback), and the Colima-wrapped `log.tail` argv. A real **camera-reachability pre-flight** is built into `migrate-colima.sh` so a networking regression fails loudly rather than silently disabling the camera.
