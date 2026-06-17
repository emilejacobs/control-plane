#!/usr/bin/env bash
#
# import-cameras.sh — one-time operator import of Edge-UI cameras into the Control Plane.
#
# For each Mac in the fleet (Tailscale IPs, one per line in mac-tailnet-ips.txt) this:
#   1. reads the CP device_id  from /var/uknomi/agent-config.json   (over SSH as uknomi@<ip>)
#   2. reads the local cameras from /usr/local/etc/uknomi/cameras.json
#   3. upserts each camera into CP via the cameras API, matching by LABEL for idempotency.
#
# cameras.json has TWO accepted shapes (both handled):
#   - Edge-UI (Flask)  : bare array   [{"id":"cam1","label":"Arrival","rtsp_url":"rtsp://...","lpr":true}, ...]
#                        (lowercase `lpr`; the per-row `id` is IGNORED — CP assigns its own camera_id)
#   - CP/agent envelope: object       {"cameras":[{"camera_id":"cam1","label":"...","rtsp_url":"...","is_lpr":true}, ...]}
# Both normalise to {label, rtsp_url, is_lpr} (object => unwrap .cameras; array => use as-is).
# The lpr/is_lpr flag is OPTIONAL: is_lpr is inferred as an explicit truthy flag OR a label
# containing "LPR" (case-insensitive). Elements missing label or rtsp_url are skipped.
#
# Idempotency: cameras are matched against CP by `label` (Edge-UI labels are a fixed predefined
# set — Arrival / Order / Pickup / Exit). Same label already present + identical fields => skip;
# present but different rtsp_url/is_lpr => PUT; absent => POST. Re-running creates no duplicates.
#
# ── Authentication ──────────────────────────────────────────────────────────────────────────
# The CP API is Bearer-JWT (staff-authed). Two ways to provide a token (no creds are hardcoded):
#   A) export CP_TOKEN=<access_token>           — use an already-obtained access token directly.
#   B) login here: set CP_EMAIL / CP_PASSWORD / CP_TOTP (any missing value is prompted for); the
#      script POSTs /auth/login and uses the returned .access_token.
#
# ── Configuration (env) ─────────────────────────────────────────────────────────────────────
#   CP_API_URL   base URL of the CP API           (default https://api.control.uknomi.com)
#   CP_TOKEN     pre-obtained access token         (path A)
#   CP_EMAIL     operator email                    (path B; prompted if unset)
#   CP_PASSWORD  operator password                 (path B; prompted, silently, if unset)
#   CP_TOTP      6-digit TOTP code                 (path B; prompted if unset)
#   IPS_FILE     fleet IP list                     (default mac-tailnet-ips.txt; or pass as $1)
#   SUDO_PW      uknomi sudo password               (prompted if unset; needed because
#                                                    /var/uknomi/agent-config.json is root-only 0600)
#   DRY_RUN=1    same as --dry-run
#
# ── Usage ───────────────────────────────────────────────────────────────────────────────────
#   # 1. ALWAYS dry-run first — prints every planned POST/PUT/skip, writes nothing:
#   CP_TOKEN=… ./import-cameras.sh --dry-run
#   # 2. Then for real:
#   CP_TOKEN=… ./import-cameras.sh
#   # Login path instead of a token:
#   CP_EMAIL=ops@uknomi.com ./import-cameras.sh --dry-run     # prompts for password + TOTP
#
# Requires: bash, ssh, curl, jq. Operator-run; not unit-tested.

set -euo pipefail

# ── Args / config ───────────────────────────────────────────────────────────────────────────
DRY_RUN="${DRY_RUN:-0}"
IPS_FILE="${IPS_FILE:-mac-tailnet-ips.txt}"
CP_API_URL="${CP_API_URL:-https://api.control.uknomi.com}"
CP_API_URL="${CP_API_URL%/}"   # strip any trailing slash

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,46p' "$0"; exit 0 ;;
    -*)        echo "unknown flag: $arg" >&2; exit 2 ;;
    *)         IPS_FILE="$arg" ;;   # positional: alternate IPS_FILE (matches restart-fleet.sh)
  esac
done

command -v curl >/dev/null || { echo "❌ curl not found" >&2; exit 1; }
command -v jq   >/dev/null || { echo "❌ jq not found"   >&2; exit 1; }
[ -f "$IPS_FILE" ] || { echo "❌ IP list not found: $IPS_FILE" >&2; exit 1; }

SSH_OPTS=(-o ConnectTimeout=8 -o BatchMode=yes -o StrictHostKeyChecking=accept-new)
# Bound every CP API call so a stalled request (e.g. a server-side cameras.update
# publish holding the response open) fails that one camera instead of hanging the
# whole run. 15s to connect, 45s total per request.
CURL_OPTS=(--connect-timeout 15 --max-time 45)

if [ "$DRY_RUN" = "1" ]; then
  echo "=== DRY RUN — no writes will be made (GETs for diffing only) ==="
else
  echo "=== LIVE RUN — cameras will be created/updated in CP ==="
fi
echo "=== CP API: $CP_API_URL ==="

# ── Auth: obtain a Bearer token ─────────────────────────────────────────────────────────────
if [ -z "${CP_TOKEN:-}" ]; then
  echo "=== No CP_TOKEN set — logging in via $CP_API_URL/auth/login ==="
  : "${CP_EMAIL:=}"; : "${CP_PASSWORD:=}"; : "${CP_TOTP:=}"
  [ -n "$CP_EMAIL" ]    || read -r  -p "CP email: " CP_EMAIL
  [ -n "$CP_PASSWORD" ] || { read -rs -p "CP password: " CP_PASSWORD; echo; }
  [ -n "$CP_TOTP" ]     || read -r  -p "TOTP code: " CP_TOTP

  login_body=$(jq -nc --arg e "$CP_EMAIL" --arg p "$CP_PASSWORD" --arg t "$CP_TOTP" \
    '{email:$e, password:$p, totp_code:$t}')
  login_resp=$(curl -fsS "${CURL_OPTS[@]}" -X POST "$CP_API_URL/auth/login" \
    -H 'Content-Type: application/json' -d "$login_body" 2>/dev/null) || {
      echo "❌ login failed (check email / password / TOTP)" >&2; exit 1; }
  CP_TOKEN=$(printf '%s' "$login_resp" | jq -r '.access_token // empty')
  [ -n "$CP_TOKEN" ] || { echo "❌ login returned no access_token" >&2; exit 1; }
  echo "=== Logged in ✅ ==="
fi
unset CP_PASSWORD 2>/dev/null || true

AUTH_HDR="Authorization: Bearer $CP_TOKEN"

# ── Sudo password ───────────────────────────────────────────────────────────────────────────
# agent-config.json is root-only (0600), so it's read with `sudo -S` (password
# piped on ssh's own stdin, exactly like restart-fleet.sh). Set SUDO_PW in the
# env to skip the prompt.
: "${SUDO_PW:=}"
[ -n "$SUDO_PW" ] || { read -rs -p "uknomi sudo password: " SUDO_PW; echo; }

# ssh_sudo_cat <ip> <remote-path> — print a root-owned file's contents over ssh
# via `sudo -S`. The password rides ssh's stdin (the printf pipe), which both
# feeds sudo AND keeps ssh off the loop's IP-list fd. <remote-path> is a fixed
# literal from this script (no untrusted input), so it's interpolated as-is.
ssh_sudo_cat() {
  # SC2029: $2 intentionally expands client-side — it's a fixed literal path
  # from this script (no untrusted input), interpolated into the remote command.
  # shellcheck disable=SC2029
  printf '%s\n' "$SUDO_PW" | ssh "${SSH_OPTS[@]}" "uknomi@$1" "sudo -S -p '' cat $2" 2>/dev/null
}

# cp_write <METHOD> <url> <json-body> — authenticated write to the CP API. On a
# 2xx returns 0; otherwise sets CP_WRITE_ERR to "HTTP <code>: <body-excerpt>"
# (or "curl: <err>" for a transport failure) and returns 1, so callers can
# surface the real reason instead of a bare "failed".
CP_WRITE_ERR=""
cp_write() {
  local method="$1" url="$2" data="$3" out code body idem
  CP_WRITE_ERR=""
  # The CP API requires an Idempotency-Key on writes. A fresh key per request is
  # fine: this script already prevents cross-run duplicates by GET-matching on
  # label before it POSTs, so each write here is a genuinely new operation.
  idem=$(uuidgen 2>/dev/null) || idem="import-${RANDOM}${RANDOM}-$(date +%s 2>/dev/null || echo 0)"
  out=$(curl -sS "${CURL_OPTS[@]}" -X "$method" "$url" \
        -H "$AUTH_HDR" -H 'Content-Type: application/json' -H "Idempotency-Key: $idem" \
        -d "$data" -w $'\n%{http_code}' 2>&1) || { CP_WRITE_ERR="curl: ${out//$'\n'/ }"; return 1; }
  code=${out##*$'\n'}
  body=${out%$'\n'*}
  case "$code" in
    2[0-9][0-9]) return 0 ;;
    *) CP_WRITE_ERR="HTTP $code: $(printf '%s' "$body" | tr '\n' ' ' | head -c 200)"; return 1 ;;
  esac
}

# ── Counters ────────────────────────────────────────────────────────────────────────────────
dev_ok=0; dev_fail=0
cam_created=0; cam_updated=0; cam_skipped=0

# ── Per-device worker. Returns non-zero on a device-level failure so the caller can `continue`.
# All output is the device's own log block. Camera counters are bumped via the global vars.
process_device() {
  local ip="$1"

  # 1. device_id — agent-config.json is root-only (0600), so read it via sudo.
  local agent_cfg device_id
  agent_cfg=$(ssh_sudo_cat "$ip" /var/uknomi/agent-config.json) || {
    echo "  ❌ unreachable, or sudo/read failed for /var/uknomi/agent-config.json — SKIP"; return 1; }
  device_id=$(printf '%s' "$agent_cfg" | jq -r '.device_id // empty' 2>/dev/null) || {
    echo "  ❌ agent-config.json is not valid JSON — SKIP"; return 1; }
  [ -n "$device_id" ] || { echo "  ❌ no .device_id in agent-config.json — SKIP"; return 1; }
  echo "  device_id: $device_id"

  # 2. local cameras. cameras.json is world-readable (0644) today, but read it
  # via sudo too so a device where the agent rewrote it 0600 still works.
  local cams_raw
  cams_raw=$(ssh_sudo_cat "$ip" /usr/local/etc/uknomi/cameras.json) || {
    echo "  ⏭️  no /usr/local/etc/uknomi/cameras.json — SKIP"; return 0; }

  # Normalise both shapes to a clean array of {label, rtsp_url, is_lpr}; drop incomplete rows.
  # The lpr/is_lpr flag is OPTIONAL on the Edge-UI side, so is_lpr is INFERRED: an explicit
  # truthy flag OR a label containing "lpr" (case-insensitive) — mirroring the Edge UI's own
  # find_lpr_camera fallback (flag first, then label match).
  local normalized
  normalized=$(printf '%s' "$cams_raw" | jq -c '
      (if type == "object" then (.cameras // []) else . end)
      | map({
          label: .label,
          rtsp_url: .rtsp_url,
          is_lpr: (
            ((.is_lpr // .lpr // false) == true)
            or ((.label // "") | ascii_downcase | contains("lpr"))
          )
        })
      | map(select((.label // "") != "" and (.rtsp_url // "") != ""))
    ' 2>/dev/null) || {
      echo "  ❌ cameras.json is not valid JSON / unexpected shape — SKIP"; return 1; }

  local count
  count=$(printf '%s' "$normalized" | jq 'length')
  if [ "$count" = "0" ]; then
    echo "  ⏭️  cameras.json has no usable cameras — SKIP"; return 0
  fi
  echo "  $count camera(s) configured locally"

  # 3. existing CP cameras for this device (for label-match upsert).
  local existing_resp existing
  existing_resp=$(curl -fsS "${CURL_OPTS[@]}" -H "$AUTH_HDR" "$CP_API_URL/devices/$device_id/cameras" 2>/dev/null) || {
    echo "  ❌ GET /devices/$device_id/cameras failed (auth/network/device not found) — SKIP"; return 1; }
  existing=$(printf '%s' "$existing_resp" | jq -c '.cameras // []' 2>/dev/null) || {
    echo "  ❌ unexpected GET cameras response — SKIP"; return 1; }

  # 4. upsert each normalized camera, matched by label.
  local i label rtsp_url is_lpr match ex_id ex_rtsp ex_lpr body
  for i in $(seq 0 $((count - 1))); do
    label=$(printf '%s'    "$normalized" | jq -r ".[$i].label")
    rtsp_url=$(printf '%s' "$normalized" | jq -r ".[$i].rtsp_url")
    is_lpr=$(printf '%s'   "$normalized" | jq    ".[$i].is_lpr")   # true/false (raw)

    body=$(jq -nc --arg l "$label" --arg u "$rtsp_url" --argjson b "$is_lpr" \
      '{label:$l, rtsp_url:$u, is_lpr:$b}')

    # Match an existing CP camera by exact label.
    match=$(printf '%s' "$existing" | jq -c --arg l "$label" 'map(select(.label == $l)) | .[0] // empty')

    if [ -n "$match" ]; then
      ex_id=$(printf   '%s' "$match" | jq -r '.camera_id')
      ex_rtsp=$(printf '%s' "$match" | jq -r '.rtsp_url')
      ex_lpr=$(printf  '%s' "$match" | jq    '.is_lpr')
      if [ "$ex_rtsp" = "$rtsp_url" ] && [ "$ex_lpr" = "$is_lpr" ]; then
        echo "  ⏭️  skip   '$label' (already up to date, $ex_id)"
        cam_skipped=$((cam_skipped + 1))
        continue
      fi
      # Differs → PUT.
      if [ "$DRY_RUN" = "1" ]; then
        echo "  ✏️  [dry-run] PUT  $ex_id  '$label'  rtsp_url/is_lpr changed"
        cam_updated=$((cam_updated + 1))
        continue
      fi
      if cp_write PUT "$CP_API_URL/devices/$device_id/cameras/$ex_id" "$body"; then
        echo "  ✅ updated '$label' ($ex_id)"
        cam_updated=$((cam_updated + 1))
      else
        echo "  ❌ PUT failed for '$label' ($ex_id) — $CP_WRITE_ERR"
        cam_skipped=$((cam_skipped + 1))
      fi
    else
      # No label match → POST (camera_id server-assigned).
      if [ "$DRY_RUN" = "1" ]; then
        echo "  ➕ [dry-run] POST '$label'  (is_lpr=$is_lpr)"
        cam_created=$((cam_created + 1))
        continue
      fi
      if cp_write POST "$CP_API_URL/devices/$device_id/cameras" "$body"; then
        echo "  ✅ created '$label'"
        cam_created=$((cam_created + 1))
      else
        echo "  ❌ POST failed for '$label' — $CP_WRITE_ERR"
        cam_skipped=$((cam_skipped + 1))
      fi
    fi
  done
  return 0
}

# ── Main loop. The IP list is read on fd 3 (not stdin) so a subprocess in the
# loop body — ssh, sudo — can never drain it off fd 0. Per-device errors are
# trapped so one bad device never aborts the run. ────────────────────────────────────────────
while read -r ip <&3 || [ -n "$ip" ]; do
  ip="${ip%%#*}"                       # strip inline comments
  ip="$(printf '%s' "$ip" | tr -d '[:space:]')"
  [ -z "$ip" ] && continue
  echo "=== $ip ==="
  if process_device "$ip"; then
    dev_ok=$((dev_ok + 1))
  else
    dev_fail=$((dev_fail + 1))
  fi
done 3< "$IPS_FILE"

# ── Summary ─────────────────────────────────────────────────────────────────────────────────
echo "================================================================"
if [ "$DRY_RUN" = "1" ]; then
  echo "DRY RUN complete — nothing was written."
fi
echo "Devices: $dev_ok processed, $dev_fail failed"
echo "Cameras: $cam_created created, $cam_updated updated, $cam_skipped skipped"
echo "================================================================"

unset CP_TOKEN SUDO_PW 2>/dev/null || true
