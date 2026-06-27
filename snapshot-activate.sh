#!/usr/bin/env bash
# snapshot-activate.sh — turn on scheduled camera snapshots across the fleet.
#
# Existing (pre-#9) devices never got `snapshot_state_path` in their agent
# config, so the snapshot scheduler is never registered — no automatic snapshots
# fire regardless of the cadence set in CP. This activates it WITHOUT re-imaging,
# in three ordered steps (this script does 1 and 3; restart-fleet.sh does 2):
#
#   ./snapshot-activate.sh backfill     # 1. POST /devices/{id}/config-backfill
#   ./restart-fleet.sh                  # 2. restart agents (sudo) so the
#                                       #    scheduler + snapshot.config handler
#                                       #    register at startup
#   ./snapshot-activate.sh cadence      # 3. PUT /devices/{id}/snapshot-config
#                                       #    (writes the cadence → scheduler fires
#                                       #    within ~1h, then per the cadence)
#
# ORDER MATTERS: a cadence push before step 2 is a silent no-op (the handler
# isn't registered yet). The cadence step re-pushes each device's CURRENT cadence
# from CP (preserving any daily/off you've set); CADENCE=weekly|daily|off forces
# one value for all.
#
# Auth (staff JWT), same as seed-alpr-creds.sh:
#   CP_TOKEN=<access_token>   OR   CP_EMAIL/CP_PASSWORD/CP_TOTP (prompted).
# Env: CP_API_URL (default prod), DRY_RUN=1 (preview, no writes), CADENCE=<val>.
# Input: a tailnet IP list (one per line, '#' comments ok); default
# mac-tailnet-ips.txt. Each IP → device_id via GET /devices. Operator-run.
set -uo pipefail

MODE="${1:-}"
case "$MODE" in
  backfill | cadence) ;;
  *) echo "usage: $0 <backfill|cadence> [ip-file]" >&2; exit 2 ;;
esac
IPS_FILE="${2:-mac-tailnet-ips.txt}"
CP_API_URL="${CP_API_URL:-https://api.control.uknomi.com}"; CP_API_URL="${CP_API_URL%/}"
DRY_RUN="${DRY_RUN:-0}"
CADENCE_OVERRIDE="${CADENCE:-}"

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

# ── tailscale_ip → "device_id cadence" map (LIST omits tailscale_ip) ─────────
echo "=== fetching device list from $CP_API_URL/devices ==="
devices_resp=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices" 2>/dev/null) \
  || { echo "❌ GET /devices failed (auth/network)" >&2; exit 1; }
IP_ID_MAP=""
while read -r did; do
  [ -z "$did" ] && continue
  detail=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices/$did" 2>/dev/null) || continue
  tsip=$(printf '%s' "$detail" | jq -r '.tailscale_ip // empty')
  cad=$(printf '%s' "$detail" | jq -r '.snapshot_cadence // "weekly"')
  [ -n "$tsip" ] && IP_ID_MAP="${IP_ID_MAP}${tsip} ${did} ${cad}"$'\n'
done < <(printf '%s' "$devices_resp" | jq -r '.devices[].device_id')
ip_row() { printf '%s' "$IP_ID_MAP" | awk -v ip="$1" '$1==ip{print; exit}'; }

ok=0; skip=0; fail=0
while read -r ip <&3 || [ -n "$ip" ]; do
  ip="${ip%%#*}"; ip="$(printf '%s' "$ip" | tr -d '[:space:]')"
  [ -z "$ip" ] && continue
  printf '%-16s ' "$ip"

  row="$(ip_row "$ip")"
  id="$(printf '%s' "$row" | awk '{print $2}')"
  cur_cad="$(printf '%s' "$row" | awk '{print $3}')"
  if [ -z "$id" ]; then
    echo "⏭️  no device with tailscale_ip=$ip — skip"; skip=$((skip + 1)); continue
  fi

  if [ "$MODE" = "backfill" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      echo "[dry-run] would POST config-backfill → $id"; ok=$((ok + 1)); continue
    fi
    idem="snap-backfill-$ip-$RANDOM"
    code="$(curl -sS "${CURL_OPTS[@]}" -o /dev/null -w '%{http_code}' \
      -X POST "$CP_API_URL/devices/$id/config-backfill" \
      -H "$AUTH_HDR" -H 'Content-Type: application/json' -H "Idempotency-Key: $idem" 2>/dev/null)"
    case "$code" in
      2*) echo "✅ config-backfill → $id"; ok=$((ok + 1)) ;;
      *) echo "❌ config-backfill HTTP $code → $id"; fail=$((fail + 1)) ;;
    esac
  else
    cad="${CADENCE_OVERRIDE:-$cur_cad}"
    case "$cad" in off | daily | weekly) ;; *) cad="weekly" ;; esac
    if [ "$DRY_RUN" = "1" ]; then
      echo "[dry-run] would PUT cadence=$cad → $id"; ok=$((ok + 1)); continue
    fi
    idem="snap-cadence-$ip-$RANDOM"
    body="$(jq -nc --arg c "$cad" '{cadence:$c}')"
    code="$(printf '%s' "$body" | curl -sS "${CURL_OPTS[@]}" -o /dev/null -w '%{http_code}' \
      -X PUT "$CP_API_URL/devices/$id/snapshot-config" \
      -H "$AUTH_HDR" -H 'Content-Type: application/json' -H "Idempotency-Key: $idem" --data @- 2>/dev/null)"
    case "$code" in
      2*) echo "✅ cadence=$cad → $id"; ok=$((ok + 1)) ;;
      *) echo "❌ snapshot-config HTTP $code → $id"; fail=$((fail + 1)) ;;
    esac
  fi
done 3< "$IPS_FILE"
echo "---- $MODE: $ok ok, $skip skipped, $fail failed ----"
