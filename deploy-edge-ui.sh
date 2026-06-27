#!/usr/bin/env bash
#
# deploy-edge-ui.sh — install the new uknomi-edge-ui (the live camera-preview
# server, port 5051) on fleet Macs that don't already have it.
#
# Background: the dashboard's "View angle" button deep-links to
# http://<device>:5051/preview/<camera_id>, served by the Go uknomi-edge-ui
# LaunchDaemon (com.uknomi.edge-ui), installed by mac-mini-rollout module
# 12-edge-ui.sh. Stores that never ran that module answer nothing on :5051, so
# the preview fails with "can't connect to the server" even though their Tailscale
# name + camera records are fine. This script closes that gap. It does NOT touch
# the agent, the old Flask UI (:5050), or anything else.
#
# IDEMPOTENT: each device is probed first (GET :5051/healthz) and SKIPPED if it's
# already serving — so a re-run only installs where it's missing.
#
# Mirrors update-agent.sh / restart-fleet.sh conventions: loops the Tailscale IPs,
# SSHes uknomi@<ip>, sudo over piped stdin. Run from the uknomi-control-plane repo
# root (needs Go + ../mac-mini-rollout for the plist).
#
# Usage:
#   ./deploy-edge-ui.sh                 # uses mac-tailnet-ips.txt
#   ./deploy-edge-ui.sh other-ips.txt   # alternate IP list
#   SUDO_PW=... ./deploy-edge-ui.sh     # skip the sudo prompt

set -uo pipefail

IPS_FILE="${1:-mac-tailnet-ips.txt}"
PLIST_SRC="../mac-mini-rollout/launchd/com.uknomi.edge-ui.plist"

command -v go  >/dev/null || { echo "❌ go not found (needed to build the binary)" >&2; exit 1; }
[ -f "$IPS_FILE" ]   || { echo "❌ IP list not found: $IPS_FILE" >&2; exit 1; }
[ -f "$PLIST_SRC" ]  || { echo "❌ plist not found: $PLIST_SRC — run from the uknomi-control-plane repo root" >&2; exit 1; }

# Build the edge-ui binary for both Mac architectures from source (the Next.js
# preview SPA is embedded; the build is self-contained).
echo "=== building uknomi-edge-ui (darwin arm64 + amd64) ==="
BIN_ARM="$(mktemp -t uknomi-edge-ui-arm64)"
BIN_AMD="$(mktemp -t uknomi-edge-ui-amd64)"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$BIN_ARM" ./cmd/uknomi-edge-ui || { echo "❌ arm64 build failed" >&2; exit 1; }
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o "$BIN_AMD" ./cmd/uknomi-edge-ui || { echo "❌ amd64 build failed" >&2; exit 1; }
echo "   built ✅"

: "${SUDO_PW:=}"
[ -n "$SUDO_PW" ] || { read -rs -p "uknomi sudo password: " SUDO_PW; echo; }

SSH_OPTS=(-o ConnectTimeout=8 -o BatchMode=yes -o StrictHostKeyChecking=accept-new)

# The remote install: place the binary + plist, (re)load the daemon, wait for it
# to serve :5051. Fed on stdin AFTER the sudo password line (sudo -S eats line 1,
# bash -s reads the rest). Kept identical to module 12-edge-ui.sh's effect.
remote_install() {
  local ip="$1" bin="$2"
  if ! scp -q "${SSH_OPTS[@]}" "$bin" "uknomi@$ip:/tmp/uknomi-edge-ui" \
     || ! scp -q "${SSH_OPTS[@]}" "$PLIST_SRC" "uknomi@$ip:/tmp/com.uknomi.edge-ui.plist"; then
    echo "  ❌ scp failed"; return 1
  fi

  { printf '%s\n' "$SUDO_PW"; cat <<'REMOTE'
set -e
install -m 0755 -o root -g wheel /tmp/uknomi-edge-ui /usr/local/bin/uknomi-edge-ui
mkdir -p /usr/local/etc/uknomi && chmod 755 /usr/local/etc/uknomi
if launchctl list com.uknomi.edge-ui >/dev/null 2>&1; then
  launchctl unload /Library/LaunchDaemons/com.uknomi.edge-ui.plist 2>/dev/null || true
fi
install -m 0644 -o root -g wheel /tmp/com.uknomi.edge-ui.plist /Library/LaunchDaemons/com.uknomi.edge-ui.plist
: > /var/log/uknomi-edge-ui-error.log
launchctl load /Library/LaunchDaemons/com.uknomi.edge-ui.plist
for _ in $(seq 1 15); do
  curl -fsS --max-time 2 http://127.0.0.1:5051/healthz >/dev/null 2>&1 && { echo SERVING; exit 0; }
  sleep 2
done
echo NOT_SERVING; tail -n 5 /var/log/uknomi-edge-ui-error.log 2>/dev/null; exit 1
REMOTE
  } | ssh "${SSH_OPTS[@]}" "uknomi@$ip" 'sudo -S -p "" bash -s'
}

ok=0; skip=0; fail=0
# IPs read on fd 3 so the inner ssh/scp can't drain the list off stdin.
while read -r ip <&3 || [ -n "$ip" ]; do
  ip="${ip%%#*}"; ip="$(printf '%s' "$ip" | tr -d '[:space:]')"
  [ -z "$ip" ] && continue
  echo "=== $ip ==="

  # Already installed + serving? skip (the "test first" gate).
  if ssh -n "${SSH_OPTS[@]}" "uknomi@$ip" 'curl -fsS --max-time 3 http://127.0.0.1:5051/healthz >/dev/null 2>&1'; then
    echo "  ⏭️  already serving on :5051 — skip"; skip=$((skip + 1)); continue
  fi

  arch=$(ssh -n "${SSH_OPTS[@]}" "uknomi@$ip" 'uname -m' 2>/dev/null)
  case "$arch" in
    arm64)  bin="$BIN_ARM" ;;
    x86_64) bin="$BIN_AMD" ;;
    *) echo "  ❌ unreachable or unknown arch '${arch:-?}' — skip"; fail=$((fail + 1)); continue ;;
  esac

  if remote_install "$ip" "$bin" | grep -q SERVING; then
    echo "  ✅ installed + serving on :5051"; ok=$((ok + 1))
  else
    echo "  ❌ install/start FAILED (check /var/log/uknomi-edge-ui-error.log on the device)"; fail=$((fail + 1))
  fi
done 3< "$IPS_FILE"

echo "================================================================"
echo "edge-ui deploy: $ok installed, $skip already had it, $fail failed/unreachable"
echo "================================================================"
rm -f "$BIN_ARM" "$BIN_AMD"
unset SUDO_PW 2>/dev/null || true
