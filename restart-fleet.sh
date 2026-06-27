#!/usr/bin/env bash
set -uo pipefail
IPS_FILE="${1:-mac-tailnet-ips.txt}"
read -rs -p "uknomi sudo password: " SUDO_PW; echo
ok=0; fail=0
while read -r ip; do
  [ -z "$ip" ] && continue
  printf '%-16s ... ' "$ip"
  if printf '%s\n' "$SUDO_PW" | ssh -o ConnectTimeout=8 -o BatchMode=yes \
       -o StrictHostKeyChecking=accept-new "uknomi@${ip}" \
       "sudo -S -p '' launchctl kickstart -k system/com.uknomi.agent" >/dev/null 2>&1; then
    echo "restarted"; ok=$((ok+1))
  else
    echo "FAILED"; fail=$((fail+1))
  fi
done < "$IPS_FILE"
echo "---- $ok restarted, $fail failed ----"
unset SUDO_PW