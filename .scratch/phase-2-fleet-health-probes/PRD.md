# PRD — Fleet health probes

**Phase:** 2
**Started:** 2026-05-27
**Status:** slice 1 implemented (2026-05-28, #19) — agent probes → MQTT → SQS → cp-ingest → Postgres → API → dashboard Health panel + per-probe-type CloudWatch alarms, all landed. Slice 2 fleet aggregate implemented (2026-05-29, #21) — `GET /fleet/alerts` + Overview alert-only roll-up (red/yellow probes + stopped services, grouped by type with drill-down). Linux backend still deferred.

## Why

The Control Plane exists to replace reactive ("a store reports plates aren't being read") with proactive ("CP saw two days ago that the container was gone"). A diagnostic session on 2026-05-27 against Eegee's store 29 Mac (`64-eegees-store29-macmini`) showed the dead-zone in concrete terms: after the 2026-05-18 reboot, auto-login of `uknomi` silently failed; the Mac sat at the login window for **9 days** with Docker Desktop down, the Plate Recognizer container not running, and the transcriber not started — and CP knew none of this. SSH worked, Tailscale was reachable, the device looked "alive."

The class of signal that surfaces problems like this is *system-state probes* that don't fit the launchctl/systemctl model. Service-status (separate PRD) catches "is the launchd job loaded?" — but it cannot see "is `uknomi` actually logged into the GUI?", "is the USB audio device enumerated?", or "is the Docker container running inside Docker's VM?" These are the probes that matter for proactive ops.

This PRD covers the slice that lands those probes.

## Scope of this PRD

Non-service system-state probes, surfaced end-to-end through the same agent → CP → dashboard pipeline that service-status uses. Initial probe set targets the failure modes observed today on the Mac Mini fleet; the surface is designed to grow.

Self-healing (e.g., a `mac-mini-rollout` LaunchDaemon that re-asserts `kcpassword` from a known-good copy) is **not** in this PRD — that's mitigation work on the device-install repo, separate from CP visibility. Probes here see the failure; mitigation lives elsewhere.

## The shape

```
agent probe collector → MQTT publish (devices/{id}/health-probes)
  → IoT Rule (filter + correlation_id pass-through)
  → SQS queue (with DLQ)
  → cp-ingest handler (sqsconsumer.Consumer[T] pattern, mirror of heartbeat)
  → Postgres UPSERT into device_health_probes
  → GET /devices/{id}/health-probes API
  → dashboard per-device Health panel (TanStack Query poll)
  → CloudWatch alarm on red probe count > 0 for >15min
```

Mirrors the `phase-2-service-status` shape exactly; same `sqsconsumer.Consumer[T]` pattern, same handler skeleton. The agent grows a `probes` package alongside the existing `service` backend; the cp-ingest container grows one more handler.

## OS-agnostic abstraction (load-bearing)

The CP-side API, dashboard, and stored signal vocabulary are **OS-agnostic**. Probe names and signal values stay constant across macOS and Linux; per-OS check commands live behind a `probes.Backend` interface inside the agent, mirroring the existing `service.Backend` pattern (`internal/service/backend_darwin.go` + `internal/service/backend_linux.go`).

CP never sees `launchctl`, `systemctl`, `kcpassword`, `system_profiler`, `lsusb`, or any other OS-specific verb. It sees probe-name → state. The agent translates.

**Slice 1 scope per OS:** the **macOS backend** ships in slice 1 (it's what the fleet runs and what today's failure modes need). The **Linux backend** is deferred per `fleet_direction` memory (Pi/Radxa consolidation away from Linux), but **the interface lands OS-agnostic from day one** so adding Linux later is a backend swap, not a refactor.

Cross-OS mapping for the probe set, kept here as the spec for the eventual Linux backend:

| Probe (OS-agnostic name) | macOS implementation (slice 1) | Linux implementation (when added) |
|---|---|---|
| `auto_login` | `defaults read … autoLoginUser` + `/etc/kcpassword` integrity | `getty@.service` autologin override + display-manager (gdm/lightdm/sddm) autologin config |
| `gui_session` | `stat -f %Su /dev/console` | `loginctl list-sessions` / `who -u` |
| `plate_recognizer_container` | `docker ps --filter name=…` | same (Docker is cross-platform) |
| `plate_recognizer_config` | sha256 + mtime of `/usr/local/etc/plate-recognizer/stream/config.ini` | same path, same hash |
| `usb_audio` | `system_profiler SPAudioDataType` / `ioreg -p IOUSB` | `lsusb` / `aplay -l` / `/proc/asound/cards` |
| `whisper_model` | glob `/usr/local/etc/uknomi/whisper-models/*.bin` | same path, same glob |
| `boot_sanity` | `kern.boottime` + wtmp | `/proc/uptime` + wtmp |

Note: the probe table below uses these OS-agnostic names; the "Check method" column documents the **macOS** implementation that ships in slice 1.

## The probe set (slice 1)

Mac-Mini-focused for the implementation; OS-agnostic in name and signal vocabulary. Order matters — top probes catch the cheapest, most consequential failures:

| Probe name | macOS check method (slice 1) | Signal vocabulary (OS-agnostic) | Catches |
|---|---|---|---|
| `auto_login` | `defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser` matches expected user **and** `/etc/kcpassword` exists with mode 600, owner root:wheel | `configured \| missing \| corrupted` | The 9-day dead-zone failure from the diagnostic session |
| `gui_session` | `stat -f %Su /dev/console` returns the expected auto-login user | `active(<user>) \| login_window \| different_user(<user>)` | Auto-login *attempted* but failed; or operator manually switched to `admin` and lingered |
| `plate_recognizer_container` | `docker ps --filter name=plate-recognizer-stream --format '{{.Status}}'` | `running \| stopped \| missing \| docker_unreachable` | Container crash, Docker daemon down, or container removed |
| `plate_recognizer_config` | sha256 of `/usr/local/etc/plate-recognizer/stream/config.ini` + mtime | `present(sha=...) \| missing` | Accidental deletion, drift from intended config |
| `usb_audio` | `system_profiler SPAudioDataType` or `ioreg -p IOUSB` matches the "Advanced USB Audio" device | `detected \| missing` | OS not enumerating the USB audio dongle (reported recurring failure) |
| `whisper_model` | Glob `/usr/local/etc/uknomi/whisper-models/*.bin`; for each file parse the filename for variant (`medium.en`, `small.en`, `large-v3`, etc.) + quantization (`q5_0`, `q8_0`, `f16`, etc.); report file size in MB | `present(variant=<parsed>, quantization=<parsed>, size_mb=N) \| missing \| multiple(<list>) \| zero_byte(<file>)` | Silent install failure (curl from HuggingFace can fail without verification); fleet-wide visibility on **which** model variant is deployed where, so future migrations to a larger/smaller model can verify rollout completed |
| `boot_sanity` | `kern.boottime` + count of reboots in last 7d via wtmp | `boots_last_7d: N, uptime_s: M` | A device rebooting every 12h is sick even if currently "up" |

**Probe-set growth path:** in slice 1 this set is hard-coded in the agent (one probe = one Go function behind a `probes.Backend` interface, with `backend_darwin.go` shipping in slice 1). Future slices add the Linux backend (cross-OS mapping above) and per-device probe overrides (ADR-031-style fleet config). All deferred — slice 1 is **seven** hard-coded probe identifiers + macOS implementations + the pipeline.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Cadence | **5-min periodic push**, same as service-status | Uniform with the existing pattern; bandwidth bounded. Most of these signals have minute-scale meaning, not second-scale. |
| Storage | **Separate `device_health_probes` table**, PK `(device_id, probe_name)`, columns include `state`, `details_jsonb` (per-probe structured payload), `last_observed_at` | Lets us answer "which devices have USB audio missing right now?" with a SQL query. JSONB on `devices` would push that into application code. Mirrors the `device_services` decision in [phase-2-service-status](../phase-2-service-status/PRD.md). |
| Topic + queue | **Separate from heartbeat and service-status**: `devices/+/health-probes` → `uknomi-cp-health-probes` SQS | Different schema, different handler, different growth path (probe set is data-driven; service-status set is allow-list-driven). Sharing a queue would couple them. |
| Slice 1 scope | **Full vertical slice** — agent probes + ingest handler + table + API + dashboard panel + one CloudWatch alarm | Operator value lands in one slice. |
| Alarm threshold (slice 1) | **Any probe red for >15 min, on >0 devices** → SNS to existing `uknomi-cp-alarms` | Same plumbing as service-status; lets us refine after operating for a few weeks. Per-probe thresholds are a slice-2 refinement. |
| What "red" means per probe | Encoded in the probe's Go function — not in CP. E.g. `usb_audio: missing` is always red; `auto_login: missing` is always red; `boots_last_7d: > 5` is red; `whisper_model: missing or zero_byte` is red, `multiple(...)` is yellow (informational — possibly mid-migration), `present(...)` is green regardless of which variant. | Keeps CP-side scoring dumb. Agent says red/yellow/green; CP stores + aggregates. The whisper probe is deliberately variant-agnostic: which model is "right" can change over time, so the agent reports facts and operators query the table. |
| **OS-agnostic surface** | Probe names + signal vocabularies are OS-agnostic; check methods live behind a `probes.Backend` interface in the agent, mirroring `service.Backend`. CP never sees OS-specific verbs. | Same principle that makes `service.restart <name>` work across macOS (launchctl) and Linux (systemctl) without CP knowing the difference. Bakes future Linux support into the design from day one without paying the implementation cost in slice 1. Avoids backing CP into a Mac-only API that would need refactoring later. |

## Out of scope (later slices / other surfaces)

- **Self-healing on the device** (e.g., re-asserting `kcpassword` from a known-good copy via a LaunchDaemon) — lives in `mac-mini-rollout`, not CP. CP sees the failure; the device-install repo can mitigate it.
- **Service-list-style probes** that are visible to launchctl/systemctl (Docker Desktop, `com.uknomi.transcriber`, `com.uknomi.docker-watchdog`) — those belong in [phase-2-service-status](../phase-2-service-status/PRD.md). This PRD is explicitly for probes that *aren't* launchctl-visible.
- **Per-device probe overrides via dashboard** — ADR-031 fleet-config pattern; slice 2 or later.
- **Pi/Radxa-specific probes** — fleet direction is Mac consolidation; no Linux-specific investment.
- **End-to-end audio capture test** — that's issue [#10](https://github.com/emilejacobs/control-plane/issues/10) (Audio test + S3 upload + CP playback). Complementary to the USB-audio probe here: this catches the *cause* (device missing); #10 catches the *symptom* (no recording). Both surfaces matter.
- **Push-on-change cadence** — phase 4 optimisation.

## Deploy gotcha (learned the hard way)

A new publish topic needs **two** changes, not one: the `sqs-ingest` module instance (IoT rule + queue) **and** the `iot:Publish` allow-list in `infra/terraform/policy.tf` (`UknomiAgentPolicy`). AWS IoT silently drops publishes to unauthorized topics — the agent looks healthy (heartbeats on `/telemetry` keep flowing) while CP receives nothing. This bit both the service-status and health-probes rollouts. The policy lives in a **separate Terraform root** (`infra/terraform/`, state key `iot-core/`), not `infra/terraform-deploy/`; update it with `terraform apply -target=aws_iot_policy.agent` (a full apply there would try to create the untracked `device.tf` resources). See the warning in [`infra/terraform/modules/README.md`](../../infra/terraform/modules/README.md).

## Constraints / honoured ADRs

- **ADR-007 (pi-radxa-minimal-agent)**: Linux fleet gets a minimal agent. The `probes.Backend` interface lands OS-agnostic from day one so the Linux backend (when added) doesn't force CP-side changes.
- **ADR-011**: structured slog + `correlation_id` stamped end-to-end on each probe-batch publish.
- **ADR-012**: integration test for the new endpoint; idempotency-on-UPSERT for the cp-ingest handler.
- **ADR-016 (telemetry retention)**: probe history honors the same retention policy as heartbeat/service-status.
- **ADR-018**: extends `cp-ingest` with a new handler — does not introduce a new container.
- **ADR-019**: Goose migration for `device_health_probes` table, embedded under `internal/cp/storage/migrations/`.
- **ADR-021**: alarm wires through the existing `uknomi-cp-alarms` SNS + per-alarm runbook under `docs/runbooks/alarms/`.

## Open questions

- **Where does the agent get sudo (or root) for the probes that need it?** The auto-login state probes can be done as `uknomi`; `defaults read` doesn't need root; `stat /dev/console` doesn't either; `system_profiler` doesn't either; `docker ps` works as `uknomi` once the docker socket is the user's. **Initial finding: none of the slice-1 probes need root.** Verify against `cmd/uknomi-agent` packaging (does the agent run as `uknomi` or as root via LaunchDaemon?). If as `root`, the probes that read user-scoped state (docker socket) need a context switch. To confirm during implementation.
- **Probe naming convention** — settled on snake_case OS-agnostic identifiers above (`auto_login`, `gui_session`, `plate_recognizer_container`, `plate_recognizer_config`, `usb_audio`, `whisper_model`, `boot_sanity`). Worth syncing with `phase-2-service-status` so its service-name vocabulary follows the same convention.
- **Does the dashboard need a fleet-wide aggregate view?** ("3 devices currently red on `auto_login`.") Slice 1 could ship per-device only and add fleet aggregate in slice 2. Operator value of per-device-only is already high; aggregate is a multiplier. (Note: whisper-variant aggregation — "show me which devices are on which model" — is a natural first aggregate; might pull it forward.)
- **Alarm noise** — if a single device flaps, do we want a single alarm or one-per-probe-type? Start with the latter (per-probe-type), refine after operating.
- **Does the OS-agnostic backend abstraction warrant its own ADR?** The same pattern governs Phase 3 service-control (`service.restart`, `service.start`, `service.stop`) and now probes. Both surfaces depend on it. An ADR ("Agent backend abstraction for OS-agnostic command + probe surface") would lock the principle in writing so future contributors don't accidentally bake `launchctl` or `systemctl` into CP-side code. Probably write alongside Phase 3 scoping (the signed-envelope ADR-013 already touches the same surface).

## Success criteria for the PRD as a whole (slice 1)

- For every Mac in the fleet, operators can open the device page and see green/yellow/red per probe with a `last_observed_at` timestamp.
- "USB audio device missing on > 0 devices for > 15 min" pages on Slack via the existing alarm SNS.
- A future cross-fleet view ("show me all devices currently in `auto_login: missing`") needs only a SQL query + a route — no agent changes.
- The next time a Mac fails the same way Eegee's store 29 did on 2026-05-18, we know within 15 minutes, not 9 days.
