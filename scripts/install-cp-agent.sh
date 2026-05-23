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
#   CP_AGENT_BIN_SRC         Path to the uknomi-agent binary to install
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

echo "enrollment ok"
