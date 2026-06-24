#!/usr/bin/env bash
# fix-colima-launchagent.sh — roll the com.uknomi.colima LaunchAgent PATH fix
# across the Colima-migrated fleet.
#
# The migrate-colima.sh / install-code plists shipped WITHOUT an
# EnvironmentVariables PATH, so launchd runs `colima start` with a minimal PATH
# and colima can't find `limactl` → the VM never auto-starts at login, the device
# reports Colima offline (it's a service in the allow-list), and a reboot leaves
# the ALPR container down. This adds the PATH to each device's existing plist and
# reloads the LaunchAgent so `colima start` succeeds (a no-op when the VM is
# already running, so it never disrupts a live container).
#
# Idempotent: a plist that already carries EnvironmentVariables:PATH is left
# untouched (only reloaded). SSHes uknomi@<ip>; no sudo needed (user LaunchAgent).
#
# Usage:
#   ./fix-colima-launchagent.sh                          # all of mac-tailnet-ips.txt
#   ./fix-colima-launchagent.sh mac-tailnet-ips-single.txt   # one device first
set -uo pipefail

IPS_FILE="${1:-mac-tailnet-ips.txt}"
[ -f "$IPS_FILE" ] || { echo "ip file not found: $IPS_FILE" >&2; exit 1; }

# Remote body runs AS uknomi. Adds EnvironmentVariables:PATH (lead with the
# colima binary's own dir, read from the plist, so it works on Apple Silicon and
# Intel), lints, reloads, and prints the LaunchAgent's resulting exit code.
REMOTE_SCRIPT=$(cat <<'REMOTE'
set -uo pipefail
PLIST="$HOME/Library/LaunchAgents/com.uknomi.colima.plist"
PB=/usr/libexec/PlistBuddy
[ -f "$PLIST" ] || { echo "no-plist"; exit 2; }
if "$PB" -c "Print :EnvironmentVariables:PATH" "$PLIST" >/dev/null 2>&1; then
  echo "already-has-path"
else
  cp "$PLIST" "$PLIST.bak"
  colima_bin="$("$PB" -c "Print :ProgramArguments:0" "$PLIST" 2>/dev/null)"
  brew_bin="$(dirname "$colima_bin")"
  "$PB" -c "Add :EnvironmentVariables dict" "$PLIST" 2>/dev/null || true
  "$PB" -c "Add :EnvironmentVariables:PATH string ${brew_bin}:/usr/bin:/bin:/usr/sbin:/sbin" "$PLIST"
  echo "added-path:${brew_bin}"
fi
plutil -lint "$PLIST" >/dev/null || { echo "lint-fail"; exit 3; }
uid_n="$(id -u)"
launchctl bootout "gui/${uid_n}/com.uknomi.colima" 2>/dev/null || true
launchctl bootstrap "gui/${uid_n}" "$PLIST" 2>/dev/null || true
sleep 8
ec="$(launchctl print "gui/${uid_n}/com.uknomi.colima" 2>/dev/null | awk -F'= ' '/last exit code/{print $2; exit}')"
echo "exit=${ec:-?}"
REMOTE
)

ok=0; fail=0
# `|| [ -n "$ip" ]` so a final line with no trailing newline (e.g.
# mac-tailnet-ips-single.txt) is still processed rather than silently skipped.
while read -r ip || [ -n "$ip" ]; do
  [ -z "$ip" ] && continue
  printf '%-16s ... ' "$ip"
  out="$(ssh -o ConnectTimeout=10 -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
            "uknomi@${ip}" 'bash -s' <<<"$REMOTE_SCRIPT" 2>&1 | tr '\n' ' ')"
  if printf '%s' "$out" | grep -q "exit=0"; then
    echo "OK    ${out}"; ok=$((ok+1))
  else
    echo "FAIL  ${out}"; fail=$((fail+1))
  fi
done < "$IPS_FILE"
echo "---- ${ok} ok, ${fail} failed ----"
