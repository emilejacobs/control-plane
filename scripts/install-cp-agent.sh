#!/usr/bin/env bash
# install-cp-agent.sh — One-page Linux install + enrollment for uknomi-agent
# (Issue 22). Wave 3 install path: download with curl, run with bash.
#
# Required env:
#   CP_BASE_URL              CP API base, e.g. https://api.control.uknomi.com
#   CP_BROKER_URL            AWS IoT MQTT URL, tls://<ats-endpoint>:8883
#   CP_BOOTSTRAP_KEY_FILE    Path to a 0600 file with the bootstrap key
#                            (the production script is rebuilt with the key
#                            baked under /etc/uknomi/bootstrap.key per ADR-017)
#   CP_AGENT_BIN_SRC         Path to the uknomi-agent binary to install. Use a
#                            release build (version stamped via -ldflags, i.e.
#                            an agent-dist artifact) so it self-reports its
#                            version correctly through self-updates (issue #39).
#   CP_SUPERVISOR_SRC        Path to uknomi-agent-supervisor.sh — the resident
#                            update wrapper (scripts/uknomi-agent-supervisor.sh).
#
# Optional env (mostly test hooks):
#   CP_AGENT_VERSION   Version string sent in the enrollment body (default "unknown")
#   CP_HARDWARE_KIND   "pi" or "radxa" (default "pi")
#   CP_ROOT            Root prefix for /etc and /usr/local (default empty = real /)
set -euo pipefail

: "${CP_BASE_URL:?CP_BASE_URL is required}"
: "${CP_BROKER_URL:?CP_BROKER_URL is required}"
: "${CP_BOOTSTRAP_KEY_FILE:?CP_BOOTSTRAP_KEY_FILE is required}"
: "${CP_AGENT_BIN_SRC:?CP_AGENT_BIN_SRC is required}"
: "${CP_SUPERVISOR_SRC:?CP_SUPERVISOR_SRC is required}"

CP_BASE_URL="${CP_BASE_URL%/}"
ROOT="${CP_ROOT:-}"
AGENT_VERSION="${CP_AGENT_VERSION:-unknown}"
HARDWARE_KIND="${CP_HARDWARE_KIND:-pi}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# Hardware UUID = systemd machine-id; stable across reboots, set once at
# image build. /etc/machine-id is the canonical path on every modern Linux.
hw_uuid="$(tr -d '[:space:]' < "${ROOT}/etc/machine-id")"
hostname="$(hostname -s 2>/dev/null || cat "${ROOT}/etc/hostname" 2>/dev/null || echo "$HARDWARE_KIND-unknown-00")"
os_version="$(awk -F= '/^PRETTY_NAME=/{gsub(/"/, "", $2); print $2}' "${ROOT}/etc/os-release" 2>/dev/null || echo "linux unknown")"

# Build the JSON body in a 0600 file so the bootstrap key never enters
# argv (visible via /proc/<pid>/cmdline) or a shell variable that could
# leak via set -x or trap dumps.
body_file="${tmpdir}/request.json"
umask 077
python3 - "$body_file" "$hostname" "$hw_uuid" "$HARDWARE_KIND" "$os_version" "$AGENT_VERSION" "$CP_BOOTSTRAP_KEY_FILE" <<'PY'
import json, os, sys
body_file, hostname, hw_uuid, kind, os_version, agent_version, key_file = sys.argv[1:8]
with open(key_file) as f:
    bootstrap_key = f.read().strip()
body = {
    "bootstrap_key": bootstrap_key,
    "hostname": hostname,
    "hardware_uuid": hw_uuid,
    "hardware_kind": kind,
    "os_version": os_version,
    "agent_version": agent_version,
}
fd = os.open(body_file, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as out:
    json.dump(body, out)
PY

resp_file="${tmpdir}/response.json"
http_code="$(curl -sS -o "$resp_file" -w '%{http_code}' \
    -X POST \
    -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${hw_uuid}" \
    --data @"$body_file" \
    --max-time 30 \
    "${CP_BASE_URL}/enrollments" 2>"${tmpdir}/curl.err" || true)"

case "$http_code" in
    200|201) ;;
    401)
        echo "enrollment rejected: bootstrap key not accepted (401)" >&2
        exit 1 ;;
    429)
        echo "enrollment rate-limited (429) — retry later" >&2
        exit 1 ;;
    000|"")
        echo "could not reach the control plane: $(cat "${tmpdir}/curl.err")" >&2
        exit 1 ;;
    *)
        echo "enrollment failed (HTTP ${http_code}): $(head -c 300 "$resp_file")" >&2
        exit 1 ;;
esac

# Lay out /etc/uknomi/: cert.pem + key.pem at 0600, agent-config.json at
# 0644 (it carries the device_id but no secret material).
runtime_dir="${ROOT}/etc/uknomi"
mkdir -p "$runtime_dir"
chmod 755 "$runtime_dir"

device_id="$(python3 - "$resp_file" "${runtime_dir}/cert.pem" "${runtime_dir}/key.pem" <<'PY'
import json, os, sys
resp_file, cert_path, key_path = sys.argv[1:4]
with open(resp_file) as f:
    resp = json.load(f)
for path, field in ((cert_path, "mtls_cert_pem"), (key_path, "mtls_private_key_pem")):
    pem = resp.get(field) or ""
    if not pem:
        sys.exit("enrollment response missing " + field)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as out:
        out.write(pem if pem.endswith("\n") else pem + "\n")
print(resp.get("device_id") or "")
PY
)"
if [[ -z "$device_id" ]]; then
    echo "enrollment response missing device_id" >&2
    exit 1
fi

# Agent config: the device_id, the IoT broker URL, paths to the cert
# material. 0644 — no secret material lands here.
cat > "${runtime_dir}/agent-config.json" <<JSON
{
  "device_id": "${device_id}",
  "version": "${AGENT_VERSION}",
  "broker_url": "${CP_BROKER_URL}",
  "client_id": "${device_id}",
  "cert_path": "/etc/uknomi/cert.pem",
  "key_path": "/etc/uknomi/key.pem",
  "ca_cert_path": "/etc/uknomi/ca.pem",
  "telemetry_interval": "30s"
}
JSON
chmod 644 "${runtime_dir}/agent-config.json"

# Lay out the resident-wrapper update root (ADR-035 §3, issue #39). The agent
# binary lives at AGENT_DIR/current; the supervisor (the systemd Program)
# health-gates any staged candidate before promoting it, and the agent writes
# AGENT_DIR/healthy once it is alive + controllable.
agent_dir="${ROOT}/var/lib/uknomi/agent-update"
mkdir -p "$agent_dir"
install -m 0755 "$CP_AGENT_BIN_SRC" "${agent_dir}/current"

# Install the supervisor wrapper at /usr/local/bin (755).
bin_dir="${ROOT}/usr/local/bin"
mkdir -p "$bin_dir"
install -m 0755 "$CP_SUPERVISOR_SRC" "${bin_dir}/uknomi-agent-supervisor"

# Write the systemd unit. systemd supervises the WRAPPER, not the agent
# directly: on launch the wrapper gates any staged candidate, then exec's
# AGENT_DIR/current. When a staged update makes the agent exit, Restart=always
# brings the wrapper back to gate the candidate. AGENT_DIR + AGENT_ARGS are the
# wrapper's contract (it word-splits AGENT_ARGS into the agent's argv);
# AGENT_ARGS is quoted so its embedded space survives systemd parsing.
unit_dir="${ROOT}/etc/systemd/system"
mkdir -p "$unit_dir"
cat > "${unit_dir}/uknomi-agent.service" <<UNIT
[Unit]
Description=uKnomi Control Plane agent (resident update wrapper)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=AGENT_DIR=/var/lib/uknomi/agent-update
Environment="AGENT_ARGS=--config /etc/uknomi/agent-config.json"
ExecStart=/usr/local/bin/uknomi-agent-supervisor
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
UNIT
chmod 644 "${unit_dir}/uknomi-agent.service"

# daemon-reload picks up the new unit, then enable + start (or restart
# if the agent was already running from a prior install).
systemctl daemon-reload
systemctl enable uknomi-agent.service
systemctl restart uknomi-agent.service

echo "enrolled ${device_id}"
