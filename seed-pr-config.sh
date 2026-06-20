#!/usr/bin/env bash
#
# seed-pr-config.sh — one-time seed of CP's per-device Plate Recognizer config
# from the config.ini files captured during the Docker→Colima migration
# (issue #5). It POSTs each captured config.ini to the NON-PUBLISHING import
# endpoint, so CP's source-of-truth matches what each device already runs —
# WITHOUT pushing anything back (no container restart). Run this BEFORE anyone
# edits a device's PR config in the dashboard, otherwise the first save pushes
# whatever empty/default values CP holds.
#
# Input: the pr-config-capture/<tailnet-ip>.config.ini files written by
# migrate-colima.sh. Each file's name is the device's Tailscale IP; the script
# resolves it to a device_id via GET /devices (tailscale_ip → id).
#
# The CP API is Bearer-JWT (staff-authed). Two ways to provide a token:
#   A) export CP_TOKEN=<access_token>
#   B) login here: set CP_EMAIL / CP_PASSWORD / CP_TOTP (missing values are
#      prompted); the script POSTs /auth/login and uses the returned token.
#
# Env:
#   CP_API_URL   base URL of the CP API   (default https://api.control.uknomi.com)
#   CAPTURE_DIR  captures directory        (default pr-config-capture)
#
# Usage:
#   CP_TOKEN=... ./seed-pr-config.sh
#   CP_EMAIL=ops@uknomi.com ./seed-pr-config.sh --dry-run   # prompts pw + TOTP
#
# Requires: bash, curl, jq. Operator-run; not unit-tested.

set -uo pipefail

CP_API_URL="${CP_API_URL:-https://api.control.uknomi.com}"
CP_API_URL="${CP_API_URL%/}"
CAPTURE_DIR="${CAPTURE_DIR:-pr-config-capture}"
DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

command -v curl >/dev/null || { echo "❌ curl not found" >&2; exit 1; }
command -v jq   >/dev/null || { echo "❌ jq not found"   >&2; exit 1; }
[ -d "$CAPTURE_DIR" ] || { echo "❌ capture dir not found: $CAPTURE_DIR" >&2; exit 1; }

CURL_OPTS=(--connect-timeout 8 --max-time 30)

# ── Auth ────────────────────────────────────────────────────────────────────
: "${CP_TOKEN:=}"
if [ -z "$CP_TOKEN" ]; then
  echo "=== No CP_TOKEN — logging in via $CP_API_URL/auth/login ==="
  : "${CP_EMAIL:=}"; : "${CP_PASSWORD:=}"; : "${CP_TOTP:=}"
  [ -n "$CP_EMAIL" ]    || read -r  -p "CP email: " CP_EMAIL
  [ -n "$CP_PASSWORD" ] || { read -rs -p "Password: " CP_PASSWORD; echo; }
  [ -n "$CP_TOTP" ]     || read -r  -p "TOTP code: " CP_TOTP
  login_body=$(jq -nc --arg e "$CP_EMAIL" --arg p "$CP_PASSWORD" --arg t "$CP_TOTP" \
    '{email:$e, password:$p, totp_code:$t, recovery_code:""}')
  login_resp=$(curl -fsS "${CURL_OPTS[@]}" -X POST "$CP_API_URL/auth/login" \
    -H 'Content-Type: application/json' -d "$login_body" 2>/dev/null) \
    || { echo "❌ login failed (email / password / TOTP)" >&2; exit 1; }
  CP_TOKEN=$(printf '%s' "$login_resp" | jq -r '.access_token // empty')
  [ -n "$CP_TOKEN" ] || { echo "❌ login returned no access_token" >&2; exit 1; }
fi
AUTH_HDR="Authorization: Bearer $CP_TOKEN"

# ── Build tailscale_ip → device_id map ──────────────────────────────────────
# The LIST endpoint (GET /devices) omits tailscale_ip — it's only on the
# per-device record — so resolve each device's detail to build the map.
echo "=== fetching device list from $CP_API_URL/devices ==="
devices_resp=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices" 2>/dev/null) \
  || { echo "❌ GET /devices failed (auth/network)" >&2; exit 1; }

n_dev=$(printf '%s' "$devices_resp" | jq '.devices | length')
echo "=== resolving tailscale IPs for $n_dev devices (per-device detail) ==="
IP_ID_MAP=""   # newline-delimited "<tailscale_ip> <device_id>" rows (bash-3.2 safe)
while read -r did; do
  [ -z "$did" ] && continue
  detail=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices/$did" 2>/dev/null) || continue
  tsip=$(printf '%s' "$detail" | jq -r '.tailscale_ip // empty')
  [ -n "$tsip" ] && IP_ID_MAP="${IP_ID_MAP}${tsip} ${did}"$'\n'
done < <(printf '%s' "$devices_resp" | jq -r '.devices[].device_id')

ip_to_id() { printf '%s' "$IP_ID_MAP" | awk -v ip="$1" '$1==ip{print $2; exit}'; }

ok=0; skip=0; fail=0
for f in "$CAPTURE_DIR"/*.config.ini; do
  [ -e "$f" ] || { echo "no *.config.ini in $CAPTURE_DIR"; break; }
  ip="$(basename "$f" .config.ini)"
  id="$(ip_to_id "$ip")"
  printf '=== %-16s ' "$ip"
  if [ -z "$id" ]; then
    echo "⏭️  no device with tailscale_ip=$ip — skip"; skip=$((skip + 1)); continue
  fi
  if [ "$DRY_RUN" = "1" ]; then
    echo "[dry-run] would import → device $id"; ok=$((ok + 1)); continue
  fi
  idem="seed-pr-$ip-$(jot -r 1 100000 999999 2>/dev/null || echo $RANDOM)"
  resp=$(curl -sS "${CURL_OPTS[@]}" -X POST "$CP_API_URL/devices/$id/pr-config/import" \
    -H "$AUTH_HDR" -H 'Content-Type: text/plain' -H "Idempotency-Key: $idem" \
    --data-binary @"$f" -w $'\n%{http_code}' 2>&1)
  code="${resp##*$'\n'}"
  if [ "$code" = "200" ]; then
    echo "✅ seeded → device $id"; ok=$((ok + 1))
  else
    echo "❌ import failed (HTTP $code): ${resp%$'\n'*}"; fail=$((fail + 1))
  fi
done

echo "================================================================"
echo "pr-config seed: $ok seeded, $skip skipped, $fail failed"
echo "================================================================"
unset CP_TOKEN CP_PASSWORD 2>/dev/null || true
