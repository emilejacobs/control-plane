#!/usr/bin/env bash
# fix-colima-launchagent.sh — roll the com.uknomi.colima LaunchAgent PATH fix
# across the Colima-migrated fleet.
#
# The migrate-colima.sh / install-code plists shipped WITHOUT an
# EnvironmentVariables PATH, so launchd runs `colima start` with a minimal PATH
# and colima can't find `limactl` → the VM never auto-starts at login, the device
# reports Colima offline (it's a service in the allow-list), and a reboot leaves
# the ALPR container down. This adds the PATH to each device's existing plist so
# the NEXT login/boot starts Colima cleanly.
#
# By default it only PATCHES the plist — it does NOT touch the running VM, so it
# never disturbs a live ALPR container and raises no transient probe alerts. The
# fix takes effect at the device's next reboot. Set RELOAD=1 to also reload the
# LaunchAgent now (clears a device that's currently showing "Colima offline" +
# verifies `colima start` exits 0) — but `colima start` briefly flickers the
# docker socket, so the plate_recognizer probe may blip "Docker Unreachable" for
# one cycle. Use RELOAD=1 per-device, not for a bulk roll.
#
# Idempotent: a plist that already carries EnvironmentVariables:PATH is left
# untouched. SSHes uknomi@<ip>; no sudo needed (user LaunchAgent).
#
# Usage:
#   ./fix-colima-launchagent.sh                          # patch all of mac-tailnet-ips.txt
#   ./fix-colima-launchagent.sh mac-tailnet-ips-single.txt   # one device first
#   RELOAD=1 ./fix-colima-launchagent.sh single.txt      # patch + reload one device now
set -uo pipefail

IPS_FILE="${1:-mac-tailnet-ips.txt}"
RELOAD="${RELOAD:-0}"
[ -f "$IPS_FILE" ] || { echo "ip file not found: $IPS_FILE" >&2; exit 1; }

# Remote body runs AS uknomi. Adds EnvironmentVariables:PATH (lead with the
# colima binary's own dir, read from the plist, so it works on Apple Silicon and
# Intel) and lints. Reloads only when RELOAD=1. Exits 0 on success; prints a
# one-word status the driver echoes.
REMOTE_SCRIPT=$(cat <<'REMOTE'
set -uo pipefail
PLIST="$HOME/Library/LaunchAgents/com.uknomi.colima.plist"
PB=/usr/libexec/PlistBuddy
[ -f "$PLIST" ] || { echo "no-plist"; exit 2; }
if "$PB" -c "Print :EnvironmentVariables:PATH" "$PLIST" >/dev/null 2>&1; then
  status="already-has-path"
else
  cp "$PLIST" "$PLIST.bak"
  colima_bin="$("$PB" -c "Print :ProgramArguments:0" "$PLIST" 2>/dev/null)"
  brew_bin="$(dirname "$colima_bin")"
  "$PB" -c "Add :EnvironmentVariables dict" "$PLIST" 2>/dev/null || true
  "$PB" -c "Add :EnvironmentVariables:PATH string ${brew_bin}:/usr/bin:/bin:/usr/sbin:/sbin" "$PLIST"
  status="patched:${brew_bin}"
fi
plutil -lint "$PLIST" >/dev/null || { echo "lint-fail"; exit 3; }
if [ "${RELOAD:-0}" = 1 ]; then
  uid_n="$(id -u)"
  launchctl bootout "gui/${uid_n}/com.uknomi.colima" 2>/dev/null || true
  launchctl bootstrap "gui/${uid_n}" "$PLIST" 2>/dev/null || true
  sleep 8
  ec="$(launchctl print "gui/${uid_n}/com.uknomi.colima" 2>/dev/null | awk -F'= ' '/last exit code/{print $2; exit}')"
  echo "${status} reloaded exit=${ec:-?}"
  [ "${ec:-1}" = 0 ] || exit 4
else
  echo "${status} patched-only"
fi
REMOTE
)

ok=0; fail=0
# `|| [ -n "$ip" ]` so a final line with no trailing newline (e.g.
# mac-tailnet-ips-single.txt) is still processed rather than silently skipped.
while read -r ip || [ -n "$ip" ]; do
  [ -z "$ip" ] && continue
  printf '%-16s ... ' "$ip"
  if out="$(ssh -o ConnectTimeout=10 -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
              "uknomi@${ip}" "RELOAD=${RELOAD} bash -s" <<<"$REMOTE_SCRIPT" 2>&1 | tr '\n' ' ')"; then
    echo "OK    ${out}"; ok=$((ok+1))
  else
    echo "FAIL  ${out}"; fail=$((fail+1))
  fi
done < "$IPS_FILE"
echo "---- ${ok} ok, ${fail} failed ----"
