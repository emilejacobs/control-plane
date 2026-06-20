#!/usr/bin/env bash
#
# seed-alpr-creds.sh — one-time seed of CP's ALPR secrets from the live fleet
# (issue #5 follow-up / ADR-036 §5). Two things, both recovered over SSH from
# each device's running Plate Recognizer container env (no CP secrets needed):
#
#   1. The PER-DEVICE license  → PUT /devices/{id}/alpr-license   (one per device)
#   2. The ACCOUNT-WIDE token   → PUT /settings/pr-token           (once, same for all)
#
# CP stores both as write-only secrets: it never echoes them back (the API only
# reports license_set / is_set). Commission (#91) is what later delivers them to
# a device — this script just makes CP's stored values match what the fleet runs
# today, so a re-commission / new rollout doesn't start from blanks.
#
# The container env is read with `docker inspect` across the colima / default /
# desktop-linux contexts, so it works on Colima-migrated devices AND the Docker
# stragglers (inspect reads a stopped container too). Devices with no
# plate-recognizer-stream container (non-ALPR, or the mis-named outage on
# 100.110.69.80) report NO_CREDS and are skipped.
#
# Input: a tailnet IP list (one IP per line, '#' comments ok), same file
# migrate-colima.sh uses. Each IP is resolved to a device_id via GET /devices
# (per-device detail — the list omits tailscale_ip).
#
# The CP API is Bearer-JWT (staff-authed). Two ways to provide a token:
#   A) export CP_TOKEN=<access_token>
#   B) login here: set CP_EMAIL / CP_PASSWORD / CP_TOTP (missing values are
#      prompted); the script POSTs /auth/login and uses the returned token.
#
# Env:
#   CP_API_URL    base URL of the CP API   (default https://api.control.uknomi.com)
#   DRY_RUN=1     recover + resolve, but PUT nothing (prints what it would do)
#   SKIP_TOKEN=1  seed per-device licenses only; don't touch the account token
#   FORCE_TOKEN=1 (re)seed the account token even if CP already has one set
#
# Usage:
#   CP_TOKEN=... ./seed-alpr-creds.sh                       # default mac-tailnet-ips.txt
#   CP_EMAIL=ops@uknomi.com DRY_RUN=1 ./seed-alpr-creds.sh other-ips.txt
#
# Secrets never hit argv (curl --data @-, jq env.*) so they can't leak via `ps`.
# Requires: bash, curl, jq, ssh. Passwordless SSH to uknomi@<ip>. Operator-run.

set -uo pipefail

CP_API_URL="${CP_API_URL:-https://api.control.uknomi.com}"
CP_API_URL="${CP_API_URL%/}"
IPS_FILE="${1:-mac-tailnet-ips.txt}"
DRY_RUN="${DRY_RUN:-0}"
SKIP_TOKEN="${SKIP_TOKEN:-0}"
FORCE_TOKEN="${FORCE_TOKEN:-0}"

command -v curl >/dev/null || { echo "❌ curl not found" >&2; exit 1; }
command -v jq   >/dev/null || { echo "❌ jq not found"   >&2; exit 1; }
command -v ssh  >/dev/null || { echo "❌ ssh not found"  >&2; exit 1; }
[ -f "$IPS_FILE" ] || { echo "❌ IP list not found: $IPS_FILE" >&2; exit 1; }

CURL_OPTS=(--connect-timeout 8 --max-time 30)
SSH_OPTS=(-o ConnectTimeout=20 -o ConnectionAttempts=2 -o ServerAliveInterval=15
  -o ServerAliveCountMax=8 -o BatchMode=yes -o StrictHostKeyChecking=accept-new)

# ── Auth ────────────────────────────────────────────────────────────────────
: "${CP_TOKEN:=}"
if [ -z "$CP_TOKEN" ]; then
  echo "=== No CP_TOKEN — logging in via $CP_API_URL/auth/login ==="
  : "${CP_EMAIL:=}"; : "${CP_PASSWORD:=}"; : "${CP_TOTP:=}"
  [ -n "$CP_EMAIL" ]    || read -r  -p "CP email: " CP_EMAIL
  [ -n "$CP_PASSWORD" ] || { read -rs -p "Password: " CP_PASSWORD; echo; }
  [ -n "$CP_TOTP" ]     || read -r  -p "TOTP code: " CP_TOTP
  login_body=$(CP_PASSWORD="$CP_PASSWORD" jq -nc --arg e "$CP_EMAIL" --arg t "$CP_TOTP" \
    '{email:$e, password:env.CP_PASSWORD, totp_code:$t, recovery_code:""}')
  login_resp=$(printf '%s' "$login_body" | curl -fsS "${CURL_OPTS[@]}" -X POST "$CP_API_URL/auth/login" \
    -H 'Content-Type: application/json' --data @- 2>/dev/null) \
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

# ── Should we seed the account token at all? ─────────────────────────────────
# Captured from the first device that yields one; seeded once after the loop.
NEED_TOKEN=0
if [ "$SKIP_TOKEN" != "1" ]; then
  if [ "$FORCE_TOKEN" = "1" ]; then
    NEED_TOKEN=1
  else
    tok_set=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/settings/pr-token" 2>/dev/null \
      | jq -r '.is_set // false')
    if [ "$tok_set" = "true" ]; then
      echo "=== account PR token already set in CP — leaving it (FORCE_TOKEN=1 to overwrite) ==="
    else
      NEED_TOKEN=1
    fi
  fi
fi
ACCOUNT_TOKEN=""

# Remote snippet: print `LICENSE_KEY=…` and `TOKEN=…` for the ALPR container, or
# NO_CREDS. Reads the container env across runtimes (Colima + Docker Desktop).
REMOTE_RECOVER='
set -u
CONTAINER="plate-recognizer-stream"
for d in /opt/homebrew/bin/docker /usr/local/bin/docker /Applications/Docker.app/Contents/Resources/bin/docker; do
  [ -x "$d" ] || continue
  for ctx in colima default desktop-linux; do
    envs="$("$d" --context "$ctx" inspect --format "{{range .Config.Env}}{{println .}}{{end}}" "$CONTAINER" 2>/dev/null)" || continue
    [ -n "$envs" ] || continue
    lic="$(printf "%s\n" "$envs" | sed -n "s/^LICENSE_KEY=//p" | head -1)"
    tok="$(printf "%s\n" "$envs" | sed -n "s/^TOKEN=//p" | head -1)"
    if [ -n "$lic" ]; then
      printf "LICENSE_KEY=%s\n" "$lic"
      printf "TOKEN=%s\n" "$tok"
      exit 0
    fi
  done
done
echo NO_CREDS
exit 1
'

ok=0; skip=0; fail=0
while read -r ip <&3 || [ -n "$ip" ]; do
  ip="${ip%%#*}"; ip="$(printf '%s' "$ip" | tr -d '[:space:]')"
  [ -z "$ip" ] && continue
  printf '=== %-16s ' "$ip"

  id="$(ip_to_id "$ip")"
  if [ -z "$id" ]; then
    echo "⏭️  no device with tailscale_ip=$ip — skip"; skip=$((skip + 1)); continue
  fi

  rec="$(ssh "${SSH_OPTS[@]}" "uknomi@$ip" 'bash -s' <<< "$REMOTE_RECOVER" 2>/dev/null)"
  # tr -d whitespace also strips any stray CR/blank the SSH channel appended; a
  # license/token never contains whitespace, so this is lossless.
  license="$(printf '%s\n' "$rec" | sed -n 's/^LICENSE_KEY=//p' | head -1 | tr -d '[:space:]')"
  token="$(printf '%s\n' "$rec" | sed -n 's/^TOKEN=//p' | head -1 | tr -d '[:space:]')"
  if [ -z "$license" ]; then
    echo "⏭️  no ALPR license recovered (NO_CREDS / SSH) — skip"; skip=$((skip + 1)); continue
  fi

  # First recovered token feeds the one-time account-token seed.
  [ "$NEED_TOKEN" = "1" ] && [ -z "$ACCOUNT_TOKEN" ] && [ -n "$token" ] && ACCOUNT_TOKEN="$token"

  if [ "$DRY_RUN" = "1" ]; then
    echo "[dry-run] would PUT license …${license: -4} → device $id"; ok=$((ok + 1)); continue
  fi

  body="$(LICENSE="$license" jq -nc '{license: env.LICENSE}')"
  if [ -z "$body" ]; then
    echo "❌ could not JSON-encode license (len ${#license}) → device $id — skip"; fail=$((fail + 1)); continue
  fi
  # CP requires an Idempotency-Key on mutating requests; a fresh key per run
  # applies the value (a replay of the same key would return the prior result).
  idem="seed-alpr-lic-$ip-$(jot -r 1 100000 999999 2>/dev/null || echo $RANDOM)"
  resp="$(printf '%s' "$body" | curl -sS "${CURL_OPTS[@]}" -w $'\n%{http_code}' \
    -X PUT "$CP_API_URL/devices/$id/alpr-license" \
    -H "$AUTH_HDR" -H 'Content-Type: application/json' -H "Idempotency-Key: $idem" --data @- 2>&1)"
  code="${resp##*$'\n'}"; rbody="${resp%$'\n'*}"
  if [ "$code" = "200" ]; then
    echo "✅ license …${license: -4} → device $id"; ok=$((ok + 1))
  else
    echo "❌ PUT alpr-license failed (HTTP $code: ${rbody//$'\n'/ }) → device $id"; fail=$((fail + 1))
  fi
done 3< "$IPS_FILE"

# ── Account-wide PR token (once) ─────────────────────────────────────────────
if [ "$SKIP_TOKEN" != "1" ] && [ "$NEED_TOKEN" = "1" ]; then
  if [ -z "$ACCOUNT_TOKEN" ]; then
    echo "⚠️  no PR token recovered from any device — account token NOT seeded"
  elif [ "$DRY_RUN" = "1" ]; then
    echo "[dry-run] would PUT account PR token …${ACCOUNT_TOKEN: -4} → /settings/pr-token"
  else
    tbody="$(TOKEN="$ACCOUNT_TOKEN" jq -nc '{token: env.TOKEN}')"
    tidem="seed-alpr-token-$(jot -r 1 100000 999999 2>/dev/null || echo $RANDOM)"
    tresp="$(printf '%s' "$tbody" | curl -sS "${CURL_OPTS[@]}" -w $'\n%{http_code}' \
      -X PUT "$CP_API_URL/settings/pr-token" \
      -H "$AUTH_HDR" -H 'Content-Type: application/json' -H "Idempotency-Key: $tidem" --data @- 2>&1)"
    tcode="${tresp##*$'\n'}"; trbody="${tresp%$'\n'*}"
    if [ "$tcode" = "200" ]; then
      echo "✅ account PR token …${ACCOUNT_TOKEN: -4} → /settings/pr-token"
    else
      echo "❌ PUT pr-token failed (HTTP $tcode: ${trbody//$'\n'/ })"
    fi
  fi
fi

echo "================================================================"
echo "alpr-creds seed: $ok licensed, $skip skipped, $fail failed"
echo "================================================================"
unset CP_TOKEN CP_PASSWORD ACCOUNT_TOKEN 2>/dev/null || true
