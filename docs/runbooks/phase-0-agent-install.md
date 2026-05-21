# Phase 0 — Installing the agent as a system service

How to install `uknomi-agent` as a managed service on macOS (LaunchDaemon) and Linux (systemd unit). This runbook is precise enough to follow without ad-hoc reasoning; everything it references lives in this repo.

## When to use this

- Bringing the dev laptop up as a long-running test device after the manual round-trip from [Issue 01](../../.scratch/phase-0-agent-spike/issues/01-first-light.md).
- Field deployments — [Issue 07 (Mac)](../../.scratch/phase-0-agent-spike/issues/07-field-deployment-mac.md) and [Issue 08 (Linux)](../../.scratch/phase-0-agent-spike/issues/08-field-deployment-linux.md).

## Prerequisites

- The agent binary built for the target platform (`make cross-build` produces `bin/agent-darwin-{arm64,amd64}` and `bin/agent-linux-arm64`).
- A valid mTLS cert + key + CA bundle for the device, produced by the [IoT Core provisioning runbook](./phase-0-iot-core-provisioning.md).
- A config JSON populated with the broker URL, device id, and cert/key paths (see `internal/config/config.go` for the schema).
- Root / sudo access on the target host.

## Filesystem conventions

| Path | Contents |
| --- | --- |
| `/usr/local/bin/uknomi-agent` | The cross-compiled binary for the target. |
| `/etc/uknomi/agent.json` | Agent config (mode `0640`, owner `root`). |
| `/etc/uknomi/certs/device.crt` | Device cert PEM. |
| `/etc/uknomi/certs/device.key` | Device key PEM (mode `0600`, owner `root`). |
| `/etc/uknomi/certs/ca.crt` | Amazon CA bundle. |
| `/var/log/uknomi-agent.log` | macOS log file. Linux uses journald. |

Create the layout once:

```bash
sudo install -d -m 0750 -o root /etc/uknomi /etc/uknomi/certs
sudo install -m 0640 -o root agent.json /etc/uknomi/agent.json
sudo install -m 0644 -o root ca.crt /etc/uknomi/certs/ca.crt
sudo install -m 0644 -o root device.crt /etc/uknomi/certs/device.crt
sudo install -m 0600 -o root device.key /etc/uknomi/certs/device.key
```

The config's `cert_path`, `key_path`, `ca_cert_path` must match the paths above.

---

## macOS — LaunchDaemon

The plist lives at [`deploy/macos/com.uknomi.agent.plist`](../../deploy/macos/com.uknomi.agent.plist). It runs as a system service (not a per-user LaunchAgent), `RunAtLoad=true`, restarts only on non-zero exit (`KeepAlive.SuccessfulExit=false`), with a 5-second `ThrottleInterval` to avoid restart storms.

### Install the binary

```bash
# Pick the right arch — `uname -m` on an Apple Silicon Mac reports "arm64".
sudo install -m 0755 -o root bin/agent-darwin-arm64 /usr/local/bin/uknomi-agent
```

### Install and load the plist

```bash
sudo install -m 0644 -o root deploy/macos/com.uknomi.agent.plist \
    /Library/LaunchDaemons/com.uknomi.agent.plist

# bootstrap into the system domain (replaces the old `launchctl load`).
sudo launchctl bootstrap system /Library/LaunchDaemons/com.uknomi.agent.plist
sudo launchctl enable system/com.uknomi.agent
```

### Verify

```bash
# Service is registered and has a non-zero PID:
sudo launchctl print system/com.uknomi.agent | head -20

# Tail the log:
sudo tail -f /var/log/uknomi-agent.log
```

You should see structured JSON lines including `"msg":"agent started"`.

### Verify crash-recovery (kill-and-restart)

```bash
sudo launchctl print system/com.uknomi.agent | grep -i pid
# Note the pid, then:
sudo kill -9 <pid>

# Within ~5s (ThrottleInterval), the service should be running again:
sudo launchctl print system/com.uknomi.agent | grep -i pid
```

The new PID should differ from the old.

### Stop / uninstall

```bash
sudo launchctl bootout system/com.uknomi.agent
sudo rm /Library/LaunchDaemons/com.uknomi.agent.plist
```

### Reading logs

- File: `sudo tail -f /var/log/uknomi-agent.log`
- Live launchd state: `sudo launchctl print system/com.uknomi.agent`

---

## Linux — systemd

The unit file lives at [`deploy/linux/uknomi-agent.service`](../../deploy/linux/uknomi-agent.service). It is `Type=simple`, `Restart=on-failure`, `RestartSec=5`, `WantedBy=multi-user.target`. Conservative hardening (`NoNewPrivileges`, `ProtectSystem=full`, `PrivateTmp`) is on by default.

### Install the binary

```bash
sudo install -m 0755 -o root bin/agent-linux-arm64 /usr/local/bin/uknomi-agent
```

### Install and enable the unit

```bash
sudo install -m 0644 -o root deploy/linux/uknomi-agent.service \
    /etc/systemd/system/uknomi-agent.service

sudo systemctl daemon-reload
sudo systemctl enable --now uknomi-agent.service
```

### Verify

```bash
sudo systemctl status uknomi-agent.service
# Active: active (running) since ...

sudo journalctl -u uknomi-agent.service -f
```

You should see structured JSON lines including `"msg":"agent started"`.

### Verify crash-recovery (kill-and-restart)

```bash
pid=$(systemctl show -p MainPID --value uknomi-agent.service)
sudo kill -9 "$pid"

# Within ~5s (RestartSec), the unit should be running again with a new PID:
systemctl show -p MainPID --value uknomi-agent.service
```

The new PID should differ from the old. `systemctl status uknomi-agent.service` should show a recent `Started uKnomi edge agent.` line in the log.

### Stop / uninstall

```bash
sudo systemctl disable --now uknomi-agent.service
sudo rm /etc/systemd/system/uknomi-agent.service
sudo systemctl daemon-reload
```

### Reading logs

- Tail: `sudo journalctl -u uknomi-agent.service -f`
- Last hour: `sudo journalctl -u uknomi-agent.service --since '1 hour ago'`

---

## What is _not_ in this runbook

- Reboot persistence (`agent comes back after device reboot`) is verified as part of [Issue 07](../../.scratch/phase-0-agent-spike/issues/07-field-deployment-mac.md) and [Issue 08](../../.scratch/phase-0-agent-spike/issues/08-field-deployment-linux.md). The bar for Issue 06 is "works on a dev box and the install docs are correct."
- Phase 1's `mac-mini-rollout/modules/11-cp-agent.sh` will template these files (substituting device id, broker URL, etc.) so they need not be edited by hand. Phase 0 ships them static.
