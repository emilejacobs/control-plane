#!/usr/bin/env bash
#
# migrate-colima.sh — once-off Docker Desktop → Colima migration for the ALPR
# (Plate Recognizer Stream) container on fleet Mac minis (issue #92, ADR-038).
#
# Background: ADR-038 retires Docker Desktop (GUI license click + settings-store
# hack + docker-watchdog) for Colima (MIT, CLI-only, per-user VM). New installs
# go straight to Colima via the install procedure; this script migrates the
# EXISTING live fleet once. It is operator-watched and idempotent: each device
# is probed first and skipped if it's already serving ALPR under Colima.
#
# Per device it:
#   1. recovers LICENSE_KEY + TOKEN from the running Docker Desktop container
#      (so no CP secrets are needed) — falls back to PR_LICENSE/PR_TOKEN env;
#   2. backs up + preserves the existing config.ini (already on the host at the
#      bind-mount path — camera RTSP URLs survive verbatim);
#   3. STOPS the Docker container (no `rm`) and LEAVES Docker Desktop installed —
#      so it's a ready fallback. `docker stop` + the container's `unless-stopped`
#      policy means it stays down (no auto-restart on reboot), so it can't
#      double-process plates alongside Colima;
#   4. installs colima + docker CLI formulae and the com.uknomi.colima
#      LaunchAgent (so the VM comes up at login), then starts the VM now;
#   5. re-runs the ALPR container under Colima with the recovered creds + the
#      same config.ini bind mount, and verifies the container reports `Up`;
#      NOTE: ALPR's :8050 UI is intentionally NOT a health signal (it doesn't
#      serve on a healthy container — see memory plate_recognizer_no_web_ui);
#      container status is the real signal, matching internal/probes.
#
# The Docker container is PRESERVED (only stopped) until you explicitly tear it
# down — so a store device keeps a fast fallback while you verify Colima:
#   * ROLLBACK=1   — instant revert: stop the Colima container, restart Docker's.
#   * REMOVE_DOCKER=1 — the COMMIT step: remove the container, watchdog, login
#                       item, and Docker Desktop. Run only once verified.
#
# IMPORTANT — run this while Docker Desktop is STILL RUNNING so the script can
# recover the per-device license/token from the live container. The script stops
# Docker itself as part of the swap. (If Docker is already down, pass
# PR_LICENSE=... PR_TOKEN=... or the device is skipped with NO_CREDS.)
#
# Mirrors deploy-edge-ui.sh / update-agent.sh conventions: loops Tailscale IPs,
# SSHes uknomi@<ip>. NOTE: unlike those, the remote body runs AS THE uknomi USER
# (Colima needs a per-user VM — never root); sudo is used only for the few
# privileged steps (chown the mount, remove Docker).
#
# Usage:
#   ./migrate-colima.sh                          # swap; Docker stopped-but-preserved
#   ./migrate-colima.sh mac-tailnet-ips-single.txt   # test device first
#   ROLLBACK=1 ./migrate-colima.sh <ips>         # revert to the Docker container
#   REMOVE_DOCKER=1 ./migrate-colima.sh <ips>    # COMMIT: uninstall Docker (verified)
#   SUDO_PW=... ./migrate-colima.sh              # skip the sudo prompt
#   PR_LICENSE=... PR_TOKEN=... ./migrate-colima.sh other-ips.txt  # creds fallback
#   MTU=1400 ./migrate-colima.sh store13.txt     # low-MTU store (pin VM+daemon MTU)

set -uo pipefail

IPS_FILE="${1:-mac-tailnet-ips.txt}"
REMOVE_DOCKER="${REMOVE_DOCKER:-0}"
ROLLBACK="${ROLLBACK:-0}"
# MTU: pin the Colima VM ifaces + docker daemon to this MTU for low-MTU stores
# (e.g. store13, en0 MTU 1400) where the VM's default 1500 black-holes registry
# packets on the constrained uplink → image pulls fail. Empty = default 1500.
MTU="${MTU:-}"

[ "$REMOVE_DOCKER" = "1" ] && [ "$ROLLBACK" = "1" ] && { echo "❌ pick one: REMOVE_DOCKER or ROLLBACK, not both" >&2; exit 1; }

[ -f "$IPS_FILE" ] || { echo "❌ IP list not found: $IPS_FILE" >&2; exit 1; }

: "${SUDO_PW:=}"
[ -n "$SUDO_PW" ] || { read -rs -p "uknomi sudo password: " SUDO_PW; echo; }

# ConnectTimeout 40 (+ a 2nd attempt): first-connect over the tailnet can need
# DERP/NAT negotiation that exceeds a tight timeout even when a manual SSH a
# moment later succeeds. ServerAlive* keeps the long remote ops (colima start +
# image pulls = minutes) from dropping on an idle link.
SSH_OPTS=(-o ConnectTimeout=40 -o ConnectionAttempts=2 -o ServerAliveInterval=15
  -o ServerAliveCountMax=20 -o BatchMode=yes -o StrictHostKeyChecking=accept-new)

# The remote body runs as the uknomi user. The local driver prepends the sudo
# password + passthrough vars as shell assignments on stdin (so they never hit
# the remote argv / ps), then the body caches sudo once with `sudo -S -v`.
# Written to a temp file (not captured via $(cat <<…)) so macOS bash 3.2's
# `bash -n` parses it — that combo trips a 3.2 heredoc-in-cmdsubst parser bug.
REMOTE_FILE="$(mktemp -t migrate-colima-remote)"
trap 'rm -f "$REMOTE_FILE"' EXIT
cat > "$REMOTE_FILE" <<'REMOTE'
set -uo pipefail
STREAM_DIR="/usr/local/etc/plate-recognizer/stream"
CONTAINER="plate-recognizer-stream"
IMAGE_BASE="platerecognizer/alpr-stream"
DOCKER_STOPPED=0   # set once we've stopped Docker; gates the auto-rollback
MIGRATION_OK=0     # set just before success; tells the rollback trap to no-op

# CRITICAL on store Macs that haven't run brew in weeks: a bare `brew install`
# triggers a full Homebrew auto-update + 30-day cleanup that is slow and floods
# the SSH session with thousands of lines — on a real link that stalls the
# connection past the keepalive and SSH drops mid-migration (leaving ALPR down).
# Disable all of it so `brew install` is a fast, quiet bottle pour.
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ENV_HINTS=1

# Size the VM to the host: ALPR inference is CPU-bound, and Docker Desktop gave
# it ~all the host's cores. Pinning 2 vCPUs tanks recognition health. Leave 2
# cores for macOS/agent/edge-ui; ~half the RAM, capped 4–8 GiB (ALPR's footprint
# is small). Computed per-device — the fleet has mixed core counts.
NCPU="$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
MEMGIB="$(( $(sysctl -n hw.memsize 2>/dev/null || echo 8589934592) / 1073741824 ))"
if [ "$NCPU" -gt 4 ]; then COLIMA_CPU=$(( NCPU - 2 )); else COLIMA_CPU=2; fi
COLIMA_MEM=$(( MEMGIB / 2 )); [ "$COLIMA_MEM" -lt 4 ] && COLIMA_MEM=4; [ "$COLIMA_MEM" -gt 8 ] && COLIMA_MEM=8
COLIMA_DISK=30

[ "$(id -u)" -ne 0 ] || { echo "REMOTE_FAIL ran as root — Colima needs the uknomi user"; exit 1; }

# Cache sudo once (used only for chown + Docker removal); fail fast on bad pw.
if ! printf '%s\n' "$SUDO_PW" | sudo -S -p '' -v 2>/dev/null; then
  echo "REMOTE_FAIL bad sudo password"; exit 1
fi

# Locate Homebrew and put it first on PATH.
if   command -v brew >/dev/null 2>&1;    then BREW="$(command -v brew)"
elif [ -x /opt/homebrew/bin/brew ];      then BREW=/opt/homebrew/bin/brew
elif [ -x /usr/local/bin/brew ];         then BREW=/usr/local/bin/brew
else echo "REMOTE_FAIL homebrew not found"; exit 1; fi
eval "$("$BREW" shellenv)"
BREW_PREFIX="$("$BREW" --prefix)"
COLIMA="$BREW_PREFIX/bin/colima"
DOCKER="$BREW_PREFIX/bin/docker"

# (Re)write + load the com.uknomi.colima user LaunchAgent so the VM comes up at
# login with the CURRENT sizing + network flags. Called in BOTH the swap and the
# already-migrated paths so a re-run refreshes a stale plist (e.g. after a manual
# resize). RunAtLoad no-ops if colima is already running.
write_colima_launchagent() {
  local la="$HOME/Library/LaunchAgents/com.uknomi.colima.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  cat > "$la" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key><string>$BREW_PREFIX/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
    <key>Label</key><string>com.uknomi.colima</string>
    <key>ProgramArguments</key>
    <array>
        <string>$COLIMA</string>
        <string>start</string>
        <string>--cpu</string><string>$COLIMA_CPU</string>
        <string>--memory</string><string>$COLIMA_MEM</string>
        <string>--disk</string><string>$COLIMA_DISK</string>
        <string>--vm-type</string><string>vz</string>
        <string>--network-address</string>
        <string>--network-preferred-route</string>
        <string>--gateway-address</string><string>192.168.211.2</string>
        <string>--mount</string><string>$STREAM_DIR:w</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>StandardOutPath</key><string>/tmp/colima.log</string>
    <key>StandardErrorPath</key><string>/tmp/colima-error.log</string>
</dict>
</plist>
PLIST
  launchctl bootout "gui/$(id -u)/com.uknomi.colima" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$la" 2>/dev/null || launchctl load -w "$la" 2>/dev/null || true
  echo "  com.uknomi.colima LaunchAgent written ($COLIMA_CPU CPU/${COLIMA_MEM}G) + loaded — VM starts at login"
}

# Route docker at the Colima daemon via DOCKER_HOST + a clean, creds-helper-free
# DOCKER_CONFIG. Docker Desktop writes credsStore=desktop into ~/.docker, whose
# helper isn't on PATH — which breaks `docker pull` (even of public images).
# Resolve the socket from the colima context (read with the normal config)
# BEFORE switching to the clean config. Call only once colima is up + $DOCKER set.
colima_docker_env() {
  local host cfg
  host="$("$DOCKER" context inspect colima --format '{{.Endpoints.docker.Host}}' 2>/dev/null)"
  [ -n "$host" ] || host="unix://$HOME/.colima/default/docker.sock"
  cfg="$(mktemp -d)"
  export DOCKER_HOST="$host"
  export DOCKER_CONFIG="$cfg"
}

arch="$(uname -m)"
case "$arch" in
  arm64|aarch64) IMAGE_TAG="$IMAGE_BASE:arm" ;;
  *)             IMAGE_TAG="$IMAGE_BASE:latest" ;;
esac

OLD_DOCKER="$(command -v docker || true)"
[ -n "$OLD_DOCKER" ] || OLD_DOCKER="/Applications/Docker.app/Contents/Resources/bin/docker"
# Docker Desktop's own context — pinned explicitly because the swap repoints the
# DEFAULT context to colima, so on a re-run plain `docker` would hit the wrong
# daemon. `context ls`/`inspect`/`ps`/`stop` need no registry creds, so the
# desktop credsStore helper isn't invoked for these.
DKCTX="$(DOCKER_HOST= "$OLD_DOCKER" context ls --format '{{.Name}}' 2>/dev/null | grep -E '^(desktop-linux|default)$' | head -1)"
DKCTX="${DKCTX:-default}"

# --- Rollback: revert to the preserved Docker container ----------------------
# Stop the Colima container, bring Docker Desktop back up, restart its container.
if [ "${ROLLBACK:-0}" = "1" ]; then
  if [ -x "$BREW_PREFIX/bin/colima" ]; then
    "$BREW_PREFIX/bin/docker" --context colima stop "$CONTAINER" >/dev/null 2>&1 || true
    echo "  stopped the Colima container"
  fi
  if ! "$OLD_DOCKER" --context "$DKCTX" info >/dev/null 2>&1; then
    open -a Docker >/dev/null 2>&1 || open /Applications/Docker.app >/dev/null 2>&1 || true
    for _ in $(seq 1 60); do "$OLD_DOCKER" --context "$DKCTX" info >/dev/null 2>&1 && break; sleep 2; done
  fi
  "$OLD_DOCKER" --context "$DKCTX" start "$CONTAINER" >/dev/null 2>&1 || true
  # Reset the DEFAULT context back to Docker Desktop — the swap set it to colima,
  # and the device's agent/probes run plain `docker ps`; left on colima they'd
  # read the empty Colima daemon and false-alarm "container down".
  "$OLD_DOCKER" context use "$DKCTX" >/dev/null 2>&1 || true
  st="$("$OLD_DOCKER" --context "$DKCTX" ps --filter "name=$CONTAINER" --format '{{.Status}}' 2>/dev/null)"
  case "$st" in
    Up*) echo "  ROLLED_BACK — Docker container back Up ($st), default context reset to $DKCTX"; echo "REMOTE_OK"; exit 0 ;;
    *)   echo "REMOTE_FAIL rollback: Docker container did not come Up (status: ${st:-gone}, context: $DKCTX)"; exit 1 ;;
  esac
fi

# --- Idempotency probe: already migrated and container Up under Colima? ------
# Health = container status `Up` (matches internal/probes); :8050 is NOT used.
already_migrated=0
if [ -x "$BREW_PREFIX/bin/colima" ] && "$BREW_PREFIX/bin/colima" status >/dev/null 2>&1; then
  st="$("$BREW_PREFIX/bin/docker" --context colima ps --filter "name=$CONTAINER" --format '{{.Status}}' 2>/dev/null)"
  case "$st" in Up*) already_migrated=1 ;; esac
fi

if [ "$already_migrated" -eq 0 ]; then
  # Auto-rollback safety net: once Docker is stopped, ANY non-success exit (a
  # failed colima start, image pull, camera pre-flight, etc.) restarts the
  # Docker container so a failed migration never leaves ALPR down at a store.
  rollback_on_fail() {
    [ "$DOCKER_STOPPED" = "1" ] && [ "$MIGRATION_OK" != "1" ] || return 0
    echo "  ↩️  auto-rollback: migration did not complete — restarting the Docker container"
    "$BREW_PREFIX/bin/docker" --context colima stop "$CONTAINER" >/dev/null 2>&1 || true
    [ -x "$OLD_DOCKER" ] && "$OLD_DOCKER" --context "$DKCTX" start "$CONTAINER" >/dev/null 2>&1 || true
    "$OLD_DOCKER" context use "$DKCTX" >/dev/null 2>&1 || true
  }
  trap rollback_on_fail EXIT

  # --- 1. Recover license/token from the Docker Desktop container ------------
  # `inspect` works on a stopped container too, so a re-run still recovers them.
  LICENSE="" ; TOKEN=""
  if [ -x "$OLD_DOCKER" ] && "$OLD_DOCKER" --context "$DKCTX" inspect "$CONTAINER" >/dev/null 2>&1; then
    envs="$("$OLD_DOCKER" --context "$DKCTX" inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER" 2>/dev/null)"
    LICENSE="$(printf '%s\n' "$envs" | sed -n 's/^LICENSE_KEY=//p')"
    TOKEN="$(printf '%s\n' "$envs" | sed -n 's/^TOKEN=//p')"
  fi
  [ -n "$LICENSE" ] || LICENSE="${PR_LICENSE:-}"
  [ -n "$TOKEN" ]   || TOKEN="${PR_TOKEN:-}"
  if [ -z "$LICENSE" ] || [ -z "$TOKEN" ]; then
    echo "REMOTE_FAIL NO_CREDS — couldn't read LICENSE_KEY/TOKEN from the Docker"
    echo "  container. Run while Docker is installed, or pass PR_LICENSE/PR_TOKEN."
    exit 1
  fi
  echo "  recovered ALPR creds (license …${LICENSE: -4})"

  # --- 2. Preserve config.ini (already on host via the existing bind mount) --
  if [ -f "$STREAM_DIR/config.ini" ]; then
    sudo cp -p "$STREAM_DIR/config.ini" "$STREAM_DIR/config.ini.pre-colima.bak"
    echo "  config.ini present — backed up to config.ini.pre-colima.bak"
  else
    echo "  ⚠️  no config.ini at $STREAM_DIR — ALPR will regenerate defaults (camera URLs lost)"
  fi

  # --- 3. Stop the Docker container — but PRESERVE it for fallback ----------
  # Only `docker stop` (no rm, Docker Desktop left installed): if Colima
  # misbehaves at the store, ROLLBACK=1 restarts this container instantly.
  # Teardown is deferred to REMOVE_DOCKER. `stop` + the container's
  # `unless-stopped` policy keeps it down (no auto-restart on reboot), so it
  # can't double-process plates alongside Colima.
  if [ -x "$OLD_DOCKER" ]; then
    "$OLD_DOCKER" --context "$DKCTX" stop "$CONTAINER" >/dev/null 2>&1 || true
  fi
  DOCKER_STOPPED=1   # arm the auto-rollback from here on
  echo "  stopped (not removed) the Docker container — preserved for fallback"

  # --- 4. Hand the mount dir to uknomi; install Colima + docker CLI ----------
  sudo chown -R "$(id -un):$(id -gn)" "$STREAM_DIR"
  # Check the actual BINARIES, not `brew list` — Docker Desktop is a cask also
  # named "docker", so `brew list docker` is a false positive that skips the
  # docker FORMULA and leaves Colima with no docker CLI (the fleet-rollout bug).
  # install_colima_docker pours both formulae and links the docker CLI (the cask
  # owns the name, so the formula may install-but-not-link). Returns non-zero if
  # either binary is still missing afterward.
  install_colima_docker() {
    [ -x "$BREW_PREFIX/bin/colima" ] || "$BREW" install colima || true
    if [ ! -x "$BREW_PREFIX/bin/docker" ]; then
      "$BREW" install --formula docker || true
      [ -x "$BREW_PREFIX/bin/docker" ] || "$BREW" link --overwrite docker >/dev/null 2>&1 || true
    fi
    [ -x "$BREW_PREFIX/bin/colima" ] && [ -x "$BREW_PREFIX/bin/docker" ]
  }
  # HOMEBREW_NO_AUTO_UPDATE=1 (set above to avoid the slow auto-update that drops
  # SSH mid-migration) means a Homebrew that hasn't been updated in a long time
  # can fail to pour current bottles — `Utils::Bottles.load_tab: undefined method
  # '[]' for nil`, or a dependency left uninstalled (e.g. colima without lima).
  # On any such failure, run `brew update` ONCE (quietly) and retry; Homebrew
  # itself says this is the fix. Gated to the failure path, so the SSH-drop risk
  # only applies to already-broken devices.
  if ! install_colima_docker; then
    echo "  ⚠️  brew install incomplete (stale-Homebrew bottle/dep failure) — 'brew update' once, then retry…"
    "$BREW" update >/dev/null 2>&1 || true
    install_colima_docker || { echo "REMOTE_FAIL colima/docker CLI missing after 'brew update' + retry"; exit 1; }
  fi

  # --- 5. Start the VM now + (re)run ALPR under Colima ----------------------
  echo "  starting Colima VM (cpu=$COLIMA_CPU mem=${COLIMA_MEM}G disk=${COLIMA_DISK}G) — first run downloads the ~320MB VM image…"
  # --network-address + --network-preferred-route: the VZNAT reachable network
  # becomes the VM's default route, which CAN reach the host's directly-connected
  # LAN (the RTSP camera). Without preferred-route the lima usernet default wins
  # and the camera is unreachable (memory colima_lan_camera_networking).
  # --gateway-address moves lima's usernet OFF its 192.168.5.0/24 default, which
  # collides with stores whose LAN is 192.168.5.x — there the VM treats the
  # camera as on-link and never reaches it. Shared mode — no socket_vmnet/sudoers.
  #
  # Retry: the VM qcow2 is fetched from GitHub on first start; at fleet scale that
  # redirect/download intermittently times out ("server may be slow or
  # overloaded"). Retry a few times (clean delete first so the network config
  # applies — colima networking is fixed at VM creation). A final failure trips
  # the auto-rollback (Docker restarted).
  colima_started=0
  for attempt in 1 2 3 4; do
    "$COLIMA" delete -f >/dev/null 2>&1 || true
    if "$COLIMA" start --cpu "$COLIMA_CPU" --memory "$COLIMA_MEM" --disk "$COLIMA_DISK" \
         --vm-type vz --network-address --network-preferred-route \
         --gateway-address 192.168.211.2 --mount "$STREAM_DIR:w"; then
      colima_started=1; break
    fi
    echo "  ⚠️  colima start attempt $attempt failed (VM image download / GitHub?) — retry in 20s…"
    sleep 20
  done
  if [ "$colima_started" -ne 1 ]; then
    echo "REMOTE_FAIL colima start failed after retries (VM image download or vz/auto-login)"; exit 1
  fi
  # Point default context at colima for the operator's later manual `docker …`.
  "$DOCKER" context use colima >/dev/null 2>&1 || true
  colima_docker_env   # talk to colima via DOCKER_HOST + a creds-helper-free config

  # Wait for the daemon.
  for _ in $(seq 1 30); do "$DOCKER" info >/dev/null 2>&1 && break; sleep 2; done
  "$DOCKER" info >/dev/null 2>&1 || { echo "REMOTE_FAIL docker daemon unreachable via Colima"; exit 1; }

  # Low-MTU stores (en0 < 1500, e.g. store13 at 1400): the Colima VM brings eth0
  # /col0 up at 1500, so registry packets sized to the VM's MSS get black-holed
  # entering the constrained uplink → the image pull below (and the pre-flight's
  # own alpine pull) stalls and fails. Pin the VM host ifaces NOW so this run's
  # pulls fit, and pin the docker daemon MTU (daemon.json, persisted on the VM
  # disk → survives reboot) so the ALPR container's internet path — license check
  # + webhook POSTs — also fits without re-touching the ifaces each boot.
  if [ -n "${MTU:-}" ]; then
    echo "  MTU=$MTU set (low-MTU store) — pinning Colima VM ifaces + docker daemon…"
    "$COLIMA" ssh -- sudo ip link set eth0 mtu "$MTU" 2>/dev/null || true
    "$COLIMA" ssh -- sudo ip link set col0 mtu "$MTU" 2>/dev/null || true
    # Merge mtu into the VM's daemon.json (preserve any colima-written settings);
    # python3 ships in the lima guest. Fall back to a fresh file if none exists.
    if "$COLIMA" ssh -- test -s /etc/docker/daemon.json 2>/dev/null; then
      "$COLIMA" ssh -- sudo python3 -c "import json;p='/etc/docker/daemon.json';d=json.load(open(p));d['mtu']=$MTU;json.dump(d,open(p,'w'))" 2>/dev/null || true
    else
      printf '{"mtu":%s}\n' "$MTU" | "$COLIMA" ssh -- sudo tee /etc/docker/daemon.json >/dev/null 2>&1 || true
    fi
    "$COLIMA" ssh -- sudo systemctl restart docker 2>/dev/null || true
    for _ in $(seq 1 30); do "$DOCKER" info >/dev/null 2>&1 && break; sleep 2; done
    "$DOCKER" info >/dev/null 2>&1 || { echo "REMOTE_FAIL docker daemon unreachable after MTU/daemon restart"; exit 1; }
  fi

  # Pre-flight: confirm a container can reach the LPR camera BEFORE pulling the
  # big ALPR image — else ALPR would just retry, disable the camera, and exit.
  # This is the failure mode that needs --network-preferred-route (above).
  CAM_HOST="$(grep -oE 'rtsp://[^[:space:]]+' "$STREAM_DIR/config.ini" 2>/dev/null | head -1 | sed -E 's#rtsp://([^@]*@)?([^:/]+).*#\2#')"
  case "$CAM_HOST" in
    ""|host.docker.internal|localhost|127.0.0.1)
      # Host-local camera (a relay on the Mac itself), not a LAN route — Colima's
      # host gateway handles it; the LAN pre-flight doesn't apply. Verified by the
      # container-Up check below instead.
      echo "  (skipping LAN pre-flight — camera host '${CAM_HOST:-none}' is host-local)" ;;
    *)
      if "$DOCKER" run --rm alpine sh -c "nc -z -w5 $CAM_HOST 554" >/dev/null 2>&1; then
        echo "  ✓ container reaches LPR camera $CAM_HOST:554"
      else
        echo "REMOTE_FAIL container cannot reach LPR camera $CAM_HOST:554 under Colima."
        echo "  Networking: expected --network-preferred-route + --gateway-address to route"
        echo "  the LAN via the reachable net. Check 'colima ls' ADDRESS + the VM routes."
        exit 1
      fi ;;
  esac

  "$DOCKER" pull "$IMAGE_TAG" || { echo "REMOTE_FAIL image pull"; exit 1; }
  "$DOCKER" rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if ! "$DOCKER" run -d --restart=unless-stopped --name "$CONTAINER" \
       -v "$STREAM_DIR:/user-data" \
       -e LICENSE_KEY="$LICENSE" -e TOKEN="$TOKEN" \
       -p 8050:8050 "$IMAGE_TAG" >/dev/null; then
    echo "REMOTE_FAIL docker run under Colima"; exit 1
  fi
  echo "  ALPR container started under Colima"
else
  colima_docker_env   # talk to colima via DOCKER_HOST + a creds-helper-free config
  echo "  already on Colima — skipping swap"
fi

# --- 6. Verify the container is Up and STAYS up (no crash-loop) -------------
# :8050 is intentionally not probed — it doesn't serve on a healthy ALPR
# container (memory plate_recognizer_no_web_ui). `Up` status is the real signal.
# DOCKER_HOST (set by colima_docker_env) points at the Colima daemon already.
running=0
for _ in $(seq 1 20); do
  st="$("$DOCKER" ps --filter "name=$CONTAINER" --format '{{.Status}}' 2>/dev/null)"
  case "$st" in Up*) running=1; break ;; esac
  sleep 3
done
if [ "$running" -ne 1 ]; then
  echo "REMOTE_FAIL ALPR container not Up under Colima — last logs:"
  "$DOCKER" logs --tail 15 "$CONTAINER" 2>&1 | sed 's/^/    /' || true
  exit 1
fi
# Re-check after a short settle to catch an immediate crash-loop.
sleep 5
st="$("$DOCKER" ps --filter "name=$CONTAINER" --format '{{.Status}}' 2>/dev/null)"
case "$st" in
  Up*) ;;
  *) echo "REMOTE_FAIL ALPR container did not stay Up (status: ${st:-gone}) — last logs:"
     "$DOCKER" logs --tail 15 "$CONTAINER" 2>&1 | sed 's/^/    /' || true
     exit 1 ;;
esac
[ -f "$STREAM_DIR/config.ini" ] && echo "  config.ini intact ✓"
echo "  ALPR container Up under Colima ($st) ✓"
MIGRATION_OK=1   # verified healthy — the auto-rollback trap will now no-op

# (Re)write + load the LaunchAgent with the current sizing/flags — fixes a stale
# plist on an already-migrated device (e.g. after a manual resize). Done AFTER the
# verify (not before): bootstrapping the agent fires its RunAtLoad `colima start`,
# and that redundant start against the just-booted VM flickers the docker socket
# — running it before the verify false-failed an otherwise-healthy migration
# (store13, where the MTU daemon restart tightened the timing).
write_colima_launchagent

# --- 7. Remove Docker Desktop (opt-in, only after verified success) ---------
if [ "${REMOVE_DOCKER:-0}" = "1" ]; then
  # The preserved Docker container is destroyed wholesale with Docker Desktop's
  # data below (app + group container), so no explicit `docker rm` is needed.
  WD_PLIST="$HOME/Library/LaunchAgents/com.uknomi.docker-watchdog.plist"
  if [ -f "$WD_PLIST" ]; then launchctl unload "$WD_PLIST" 2>/dev/null || true; rm -f "$WD_PLIST"; fi
  rm -f /usr/local/bin/docker-watchdog.sh 2>/dev/null || true
  osascript -e 'tell application "System Events" to delete login item "Docker"' >/dev/null 2>&1 || true
  osascript -e 'quit app "Docker"' >/dev/null 2>&1 || true
  if "$BREW" list --cask docker >/dev/null 2>&1; then
    "$BREW" uninstall --cask docker >/dev/null 2>&1 || echo "  ⚠️  brew uninstall --cask docker failed — remove /Applications/Docker.app manually"
  else
    sudo rm -rf /Applications/Docker.app 2>/dev/null || true
  fi
  rm -rf "$HOME/Library/Group Containers/group.com.docker" 2>/dev/null || true
  echo "  removed Docker Desktop + watchdog + login item"
fi

echo "REMOTE_OK"
REMOTE

ok=0; skip=0; fail=0
# Capture each device's existing PR config.ini for later seeding into CP (so the
# first CP-driven config push doesn't clobber hand-tuned settings). config.ini is
# world-readable (0644) — no sudo needed.
CAPTURE_DIR="pr-config-capture"
[ "$ROLLBACK" = "1" ] || mkdir -p "$CAPTURE_DIR"
# IPs read on fd 3 so the inner ssh can't drain the list off stdin.
while read -r ip <&3 || [ -n "$ip" ]; do
  ip="${ip%%#*}"; ip="$(printf '%s' "$ip" | tr -d '[:space:]')"
  [ -z "$ip" ] && continue
  echo "=== $ip ==="

  # Prepend the secrets as shell assignments on stdin (never on argv/ps), then
  # the remote body. %q keeps arbitrary passwords/tokens safe.
  out=$( { printf 'SUDO_PW=%q\nREMOVE_DOCKER=%q\nROLLBACK=%q\nPR_LICENSE=%q\nPR_TOKEN=%q\nMTU=%q\n' \
             "$SUDO_PW" "$REMOVE_DOCKER" "$ROLLBACK" "${PR_LICENSE:-}" "${PR_TOKEN:-}" "${MTU:-}"; \
           cat "$REMOTE_FILE"; } \
        | ssh "${SSH_OPTS[@]}" "uknomi@$ip" 'bash -s' 2>&1)
  echo "$out" | grep -v '^REMOTE_OK$' | sed 's/^/  /'

  if echo "$out" | grep -q '^REMOTE_OK$'; then
    if echo "$out" | grep -q 'ROLLED_BACK'; then
      echo "  ↩️  rolled back to Docker"; ok=$((ok + 1))
    elif echo "$out" | grep -q 'already on Colima — skipping swap'; then
      echo "  ⏭️  already migrated"; skip=$((skip + 1))
    else
      echo "  ✅ migrated to Colima"; ok=$((ok + 1))
    fi
  else
    echo "  ❌ FAILED"; fail=$((fail + 1))
    # Safety net: a failed run may have stopped Docker without the remote
    # auto-rollback firing (e.g. the SSH session dropped → remote SIGHUP skips
    # the EXIT trap). Restart the preserved Docker container from a FRESH
    # connection so a store is never left down. Idempotent: a no-op if Docker is
    # already up / was never stopped / the device is on Colima.
    if [ "$ROLLBACK" != "1" ] && [ "$REMOVE_DOCKER" != "1" ]; then
      if ssh "${SSH_OPTS[@]}" "uknomi@$ip" 'd=/Applications/Docker.app/Contents/Resources/bin/docker; [ -x "$d" ] || exit 0; c=$("$d" context ls --format "{{.Name}}" 2>/dev/null | grep -E "^(desktop-linux|default)$" | head -1); c="${c:-default}"; "$d" context use "$c" >/dev/null 2>&1; "$d" --context "$c" start plate-recognizer-stream >/dev/null 2>&1' 2>/dev/null; then
        echo "     ↩️  safety-net: ensured Docker container is running"
      fi
    fi
  fi

  # Seed-capture the live config.ini (skip on rollback).
  if [ "$ROLLBACK" != "1" ] && echo "$out" | grep -q '^REMOTE_OK$'; then
    if scp -q "${SSH_OPTS[@]}" "uknomi@$ip:/usr/local/etc/plate-recognizer/stream/config.ini" "$CAPTURE_DIR/$ip.config.ini" 2>/dev/null; then
      echo "  📥 captured config.ini → $CAPTURE_DIR/$ip.config.ini (for CP seeding)"
    else
      echo "  ⚠️  could not capture config.ini from $ip"
    fi
  fi
done 3< "$IPS_FILE"

echo "================================================================"
if [ "$ROLLBACK" = "1" ]; then
  echo "colima rollback: $ok reverted, $fail failed"
else
  echo "colima migration: $ok migrated, $skip already on colima, $fail failed"
  if [ "$REMOVE_DOCKER" = "1" ]; then
    echo "(Docker Desktop removed on success)"
  else
    echo "(Docker stopped-but-preserved — ROLLBACK=1 to revert, REMOVE_DOCKER=1 to commit once verified)"
  fi
fi
echo "================================================================"
unset SUDO_PW 2>/dev/null || true
