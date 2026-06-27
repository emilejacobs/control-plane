#!/usr/bin/env bash
# Update old-layout Mac agents to the self-updating supervisor layout at a given version.
set -uo pipefail
VERSION="${VERSION:?set VERSION, e.g. VERSION=1.4.6 (1.4.5 still has the reconnect bug)}"
BUCKET="uknomi-cp-agent-dist-523612763411"
SUP_SRC="../mac-mini-rollout/bin/uknomi-agent-supervisor"
MIG_SRC="../mac-mini-rollout/migrate-cp-agent-supervisor.sh"
TARGETS=("$@")
[ ${#TARGETS[@]} -gt 0 ] || { echo "usage: VERSION=1.4.6 $0 <ip> [<ip>...]"; exit 1; }
[ -f "$SUP_SRC" ] && [ -f "$MIG_SRC" ] || { echo "run from uknomi-control-plane/ (needs ../mac-mini-rollout)"; exit 1; }
read -rs -p "uknomi sudo password: " PW; echo

for ip in "${TARGETS[@]}"; do
  echo "=== $ip ==="
  arch=$(ssh -o BatchMode=yes -o ConnectTimeout=8 "uknomi@$ip" 'uname -m' 2>/dev/null)
  case "$arch" in
    arm64)  bin="uknomi-agent-darwin-arm64" ;;
    x86_64) bin="uknomi-agent-darwin-amd64" ;;
    *) echo "  unknown/unreachable arch '$arch' — skipping"; continue ;;
  esac
  local_bin="/tmp/uknomi-agent-${VERSION}-${arch}"
  [ -f "$local_bin" ] || aws s3 cp "s3://$BUCKET/agent/$VERSION/$bin" "$local_bin" --region us-east-1 >/dev/null \
    || { echo "  download failed (does version $VERSION exist?)"; continue; }
  chmod +x "$local_bin"
  scp -q "$local_bin" "uknomi@$ip:/tmp/uknomi-agent" \
    && scp -q "$SUP_SRC" "uknomi@$ip:/tmp/uknomi-agent-supervisor" \
    && scp -q "$MIG_SRC" "uknomi@$ip:/tmp/migrate-cp-agent-supervisor.sh" \
    || { echo "  scp failed"; continue; }
  if printf '%s\n' "$PW" | ssh "uknomi@$ip" \
      "sudo -S -p '' env CP_AGENT_BIN_SRC=/tmp/uknomi-agent CP_SUPERVISOR_SRC=/tmp/uknomi-agent-supervisor \
       bash /tmp/migrate-cp-agent-supervisor.sh"; then
    echo "  ✅ migrated to $VERSION (supervisor layout — self-updates from here)"
  else
    echo "  ❌ migrate FAILED"
  fi
done
unset PW