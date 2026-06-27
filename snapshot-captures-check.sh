#!/usr/bin/env bash
# snapshot-captures-check.sh — read-only verify that scheduled snapshots fired.
#
# For each device (by tailnet IP → device_id), GET /devices/{id}/captures?kind=snapshot
# and report the NEWEST snapshot's timestamp. A snapshot dated TODAY means the
# scheduler fired (nobody ran on-demand today); only-stale (e.g. 06-17) means it
# did not. Purely read-only — no writes, no SSH. Operator-run (staff JWT).
#
# Auth (same as snapshot-activate.sh / seed-alpr-creds.sh):
#   CP_TOKEN=<access_token>   OR   CP_EMAIL/CP_PASSWORD/CP_TOTP (prompted).
# Env: CP_API_URL (default prod). Input: tailnet IP list (default mac-tailnet-ips.txt).
set -uo pipefail

IPS_FILE="${1:-mac-tailnet-ips.txt}"
CP_API_URL="${CP_API_URL:-https://api.control.uknomi.com}"; CP_API_URL="${CP_API_URL%/}"
TODAY="$(date -u +%Y-%m-%d)"

command -v curl >/dev/null || { echo "curl required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq required" >&2; exit 1; }
[ -f "$IPS_FILE" ] || { echo "ip file not found: $IPS_FILE" >&2; exit 1; }

CURL_OPTS=(--connect-timeout 8 --max-time 30)

# ── Auth ──────────────────────────────────────────────────────────────────
: "${CP_TOKEN:=}"
if [ -z "$CP_TOKEN" ]; then
  echo "=== No CP_TOKEN — logging in via $CP_API_URL/auth/login ==="
  : "${CP_EMAIL:=}"; : "${CP_PASSWORD:=}"; : "${CP_TOTP:=}"
  [ -n "$CP_EMAIL" ] || read -r -p "CP email: " CP_EMAIL
  [ -n "$CP_PASSWORD" ] || { read -rs -p "Password: " CP_PASSWORD; echo; }
  [ -n "$CP_TOTP" ] || read -r -p "TOTP code: " CP_TOTP
  login_body=$(CP_PASSWORD="$CP_PASSWORD" jq -nc --arg e "$CP_EMAIL" --arg t "$CP_TOTP" \
    '{email:$e, password:env.CP_PASSWORD, totp_code:$t, recovery_code:""}')
  login_resp=$(printf '%s' "$login_body" | curl -fsS "${CURL_OPTS[@]}" -X POST "$CP_API_URL/auth/login" \
    -H 'Content-Type: application/json' --data @- 2>/dev/null) \
    || { echo "❌ login failed (email / password / TOTP)" >&2; exit 1; }
  CP_TOKEN=$(printf '%s' "$login_resp" | jq -r '.access_token // empty')
  [ -n "$CP_TOKEN" ] || { echo "❌ login returned no access_token" >&2; exit 1; }
fi
AUTH_HDR="Authorization: Bearer $CP_TOKEN"

# ── tailscale_ip → device_id map (LIST omits tailscale_ip, so fetch detail) ──
echo "=== fetching device list from $CP_API_URL/devices ==="
devices_resp=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices" 2>/dev/null) \
  || { echo "❌ GET /devices failed (auth/network)" >&2; exit 1; }
IP_ID_MAP=""
while read -r did; do
  [ -z "$did" ] && continue
  detail=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices/$did" 2>/dev/null) || continue
  tsip=$(printf '%s' "$detail" | jq -r '.tailscale_ip // empty')
  [ -n "$tsip" ] && IP_ID_MAP="${IP_ID_MAP}${tsip} ${did}"$'\n'
done < <(printf '%s' "$devices_resp" | jq -r '.devices[].device_id')
ip_id() { printf '%s' "$IP_ID_MAP" | awk -v ip="$1" '$1==ip{print $2; exit}'; }

echo "=== newest snapshot per device (TODAY = $TODAY UTC) ==="
fired=0; stale=0; none=0; skip=0
while read -r ip <&3 || [ -n "$ip" ]; do
  ip="${ip%%#*}"; ip="$(printf '%s' "$ip" | tr -d '[:space:]')"
  [ -z "$ip" ] && continue
  printf '%-16s ' "$ip"
  id="$(ip_id "$ip")"
  if [ -z "$id" ]; then echo "⏭️  no device for ip — skip"; skip=$((skip+1)); continue; fi

  resp=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" \
    "$CP_API_URL/devices/$id/captures?kind=snapshot" 2>/dev/null) || { echo "❌ list failed"; continue; }
  newest=$(printf '%s' "$resp" | jq -r '.captures[0].created_at // empty')
  count=$(printf '%s' "$resp" | jq -r '.captures | length')
  if [ -z "$newest" ]; then echo "⚪ no snapshots at all"; none=$((none+1)); continue; fi
  day="${newest%%T*}"
  if [ "$day" = "$TODAY" ]; then
    echo "✅ fired today — newest=$newest ($count total)"; fired=$((fired+1))
  else
    echo "⚠️  stale — newest=$newest ($count total)"; stale=$((stale+1))
  fi
done 3< "$IPS_FILE"
echo "---- fired-today=$fired stale=$stale no-snapshots=$none skipped=$skip ----"
