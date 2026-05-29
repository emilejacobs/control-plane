# Alarm — `uknomi-cp-health-probe-<probe>`

One alarm per probe type (per the #19 triage decision — a single roll-up would hide *which* signal is red). The probe set: `auto_login`, `gui_session`, `plate_recognizer_container`, `plate_recognizer_config`, `usb_audio`, `whisper_model`, `boot_sanity`.

**Fires when**: ≥1 `health-probe.red` log line for that probe landed in cp-ingest across 3 consecutive 5-minute reporting windows (the probe has been red on ≥1 device for ~15 minutes). The alarm name tells you which probe; the log lines tell you which device(s).

**Why it matters**: these are the *non-service* system-state signals the launchctl/systemctl model can't see — the class of failure behind the 2026-05-18 Eegee's-store-29 dead-zone, where a Mac sat at the login window for 9 days looking alive over SSH/Tailscale. The whole point of #19 is that we now know within ~15 minutes.

**OS-agnostic by construction (ADR-034)**: the probe names and signal vocabulary are the agent's, not the OS's. CP never sees `launchctl`/`defaults`/`kcpassword`. When the Linux backend lands the same names mean the same things.

## What to check first (any probe)

1. **Which device(s), and what state?**
   ```bash
   aws logs tail /uknomi/cp-ingest --since 30m \
     --filter-pattern '"health-probe.red"' --format short
   ```
   Each line carries `device_id`, `probe`, `state`, `correlation_id`.

2. **Per-device snapshot** via the API or the dashboard Health panel:
   ```bash
   curl -sS "https://api.control.uknomi.com/devices/<device_id>/health-probes" \
     -H "Authorization: Bearer $(./scripts/mint-token.sh)" | jq '.probes[] | select(.status=="red")'
   ```
   The `details` object carries the structured payload (config sha256, whisper variant/size, boot count, etc.).

3. **Is the device online at all?** `GET /devices/<device_id>` → `.is_online`. An offline device's probe rows are stale; chase connectivity first.

## Per-probe meaning + next step

| Probe | Red state(s) | What it means | First move |
|---|---|---|---|
| `auto_login` | `missing` / `corrupted` | `autoLoginUser` not the expected user, or `/etc/kcpassword` absent / wrong perms. The decay failure: macOS security maintenance wipes `kcpassword` between reboots. | On the device: `defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser` and `ls -l /etc/kcpassword`. Re-assert with `sysadminctl -autoLogin set` (mitigation belongs in `mac-mini-rollout`, not CP). |
| `gui_session` | `login_window` | Auto-login *attempted but failed* — `/dev/console` owned by root, nobody in the GUI. Everything user-session-gated (Docker Desktop, Plate Recognizer, transcriber) is down. | Almost always co-fires with `auto_login`. **Do not RD in before capturing post-boot SSH state** — RD'ing masks whether auto-login engaged. |
| `gui_session` | `different_user` (yellow, not red) | An operator switched to another user and lingered. Informational. | Confirm with whoever was on the box. |
| `plate_recognizer_container` | `stopped` / `missing` / `docker_unreachable` | PR container not `Up`. `docker_unreachable` from the root agent daemon usually means Docker Desktop's per-user daemon is down — i.e. nobody logged in (check `gui_session`). | `docker ps -a --filter name=plate-recognizer-stream`. If the GUI session is healthy but the container is down, restart it. |
| `plate_recognizer_config` | `missing` | `config.ini` gone from `/usr/local/etc/plate-recognizer/stream/`. | The on-disk config is the source of truth (no usable web UI). Restore from the intended config; compare `details.sha256` against the known-good. |
| `usb_audio` | `missing` | OS not enumerating the "Advanced USB Audio" dongle. Recurring macOS enumeration failure. | Reseat the dongle / reboot. Complementary to #10 (which catches the *symptom* — no recording). |
| `whisper_model` | `missing` / `zero_byte` | Model absent or a truncated download (curl-from-HuggingFace failed with no verification). `multiple` is yellow (mid-migration), not red. | Re-download the model; verify size against `details.size_mb`. |
| `boot_sanity` | `flapping` | > 5 reboots in the last 7 days — the device is sick even if currently up. | Check power, thermal, panic logs on the device. |

## Recovery

No automatic recovery in this slice — these probes are *visibility*, not mitigation (per the PRD: self-healing lives in `mac-mini-rollout`, not CP). The per-probe table above is the manual path. The follow-up self-healing LaunchDaemon for `auto_login` is tracked separately against the install repo.

## Tuning the alarm

Threshold is "red across 3 consecutive 5-min windows" = 15 min, set by `evaluation_periods` in `infra/terraform-deploy/alarms.tf` (the `health_probe_red` for_each block). If a probe proves noisy, bump its window there. Per-probe thresholds (different windows per probe) are a slice-2 refinement.

## Related

- PRD: `.scratch/phase-2-fleet-health-probes/PRD.md`
- [ADR-034](../../adr/0034-agent-backend-abstraction-os-agnostic-surface.md) — OS-agnostic agent backend abstraction
- [health-probes-dlq.md](health-probes-dlq.md) — the ingest DLQ alarm
- Issue [#10](https://github.com/emilejacobs/control-plane/issues/10) — audio-test E2E (symptom-side of `usb_audio`)
