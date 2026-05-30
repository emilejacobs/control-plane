#!/bin/sh
# uknomi-agent-supervisor — the resident wrapper that owns safe agent
# self-update (issue #39, ADR-035 §3). The LaunchDaemon/systemd unit runs
# THIS, not the agent binary directly. On launch it health-gates any staged
# update candidate: run the candidate, wait up to HEALTH_TIMEOUT for the agent
# to prove itself alive+controllable (the health marker), then PROMOTE it
# (saving the prior binary as last-known-good) or REVERT to last-known-good
# and record a rollback. A broken new binary never decides its own fate — this
# older, stable wrapper does, off an observable marker.
#
# Layout under AGENT_DIR:
#   current             the binary normally run
#   candidate           a staged update (written by the agentupdate handler)
#   candidate.version   the candidate's version string
#   trying              flag: a candidate is awaiting its health gate
#   healthy             written by the running agent after alive+controllable
#                       (mTLS connect + cmd-subscribe + one heartbeat); content
#                       is the agent's version
#   last-known-good     the previous current, kept for rollback
#   rollback.log        appended on each rollback (telemetry source)
#
# POSIX sh, shellcheck-clean. SUPERVISOR_GATE_ONLY=1 resolves the candidate and
# exits (for tests) instead of exec'ing the agent.
set -eu

AGENT_DIR="${AGENT_DIR:?AGENT_DIR is required}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-300}"
HEALTH_POLL="${HEALTH_POLL:-2}"
# AGENT_ARGS is intentionally word-split into the agent's argv.
AGENT_ARGS="${AGENT_ARGS:-}"

log() { echo "uknomi-agent-supervisor: $*" >&2; }

# resolve_candidate health-gates a staged candidate, if any. Promotes it to
# current (saving the old current as last-known-good) when the agent reports
# healthy in time, else discards it and records a rollback. Leaves `current`
# as the binary to run either way.
resolve_candidate() {
  trying="${AGENT_DIR}/trying"
  cand="${AGENT_DIR}/candidate"
  [ -f "$trying" ] && [ -x "$cand" ] || return 0

  ver="$(cat "${AGENT_DIR}/candidate.version" 2>/dev/null || echo unknown)"
  rm -f "${AGENT_DIR}/healthy"
  log "gating candidate ${ver} (timeout ${HEALTH_TIMEOUT}s)"

  # Run the candidate so it can prove itself. Export AGENT_DIR so the agent
  # knows where to write its health marker. Its stdout/stderr go to a log so a
  # candidate (or a child it spawns) can't hold a parent's pipe open.
  AGENT_DIR="$AGENT_DIR" "$cand" $AGENT_ARGS >> "${AGENT_DIR}/candidate.log" 2>&1 &
  cand_pid=$!

  healthy=0
  waited=0
  while [ "$waited" -lt "$HEALTH_TIMEOUT" ]; do
    if [ -f "${AGENT_DIR}/healthy" ] && [ "$(cat "${AGENT_DIR}/healthy")" = "$ver" ]; then
      healthy=1
      break
    fi
    kill -0 "$cand_pid" 2>/dev/null || break   # candidate exited before healthy
    sleep "$HEALTH_POLL"
    waited=$((waited + HEALTH_POLL))
  done

  kill "$cand_pid" 2>/dev/null || true
  wait "$cand_pid" 2>/dev/null || true

  if [ "$healthy" -eq 1 ]; then
    [ -e "${AGENT_DIR}/current" ] && cp -p "${AGENT_DIR}/current" "${AGENT_DIR}/last-known-good"
    cp "$cand" "${AGENT_DIR}/current"
    chmod +x "${AGENT_DIR}/current"
    rm -f "$cand" "${AGENT_DIR}/candidate.version" "$trying" "${AGENT_DIR}/healthy"
    log "promoted ${ver}"
  else
    rm -f "$cand" "${AGENT_DIR}/candidate.version" "$trying" "${AGENT_DIR}/healthy"
    echo "${ver}" >> "${AGENT_DIR}/rollback.log"
    touch "${AGENT_DIR}/rolled-back"
    log "rolled back ${ver} — reverted to last-known-good"
  fi
}

resolve_candidate

[ -n "${SUPERVISOR_GATE_ONLY:-}" ] && exit 0

# Hand off to the (now-resolved) current binary. exec so the agent becomes the
# process the init system supervises for liveness/restart.
exec "${AGENT_DIR}/current" $AGENT_ARGS
